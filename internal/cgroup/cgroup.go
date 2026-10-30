package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	controllerFile = "cgroup.controllers"
	subtreeFile    = "cgroup.subtree_control"
	procsFile      = "cgroup.procs"
	maxValue       = "max"
	defaultWeight  = 100
	maxIOLimits    = 64
)

var managedControllers = []string{"cpu", "cpuset", "memory", "io"}

// Manager applies host limits and creates the class hierarchy.
type Manager struct {
	mu         sync.Mutex
	root       string
	mount      string
	settings   Settings
	configured bool
	lastError  string
}

// New returns a cgroup manager rooted at root. When root is empty DefaultRoot
// is used. A missing cgroup v2 mount is reported through Status and Configure
// rather than preventing the API from starting.
func New(root string) *Manager {
	root = strings.TrimSpace(root)
	if root == "" {
		root = DefaultRoot
	}
	mount, _ := cgroup2Mount()
	return &Manager{root: filepath.Clean(root), mount: mount}
}

// Root returns the configured parent cgroup path.
func (m *Manager) Root() string {
	if m == nil {
		return ""
	}
	return m.root
}

// Settings returns the currently configured host settings.
func (m *Manager) Settings() Settings {
	if m == nil {
		return Settings{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSettings(m.settings)
}

// Status returns cgroup v2 availability and controller information.
func (m *Manager) Status() Status {
	if m == nil {
		return Status{Reason: "cgroup manager is not configured", CheckedAt: time.Now().UTC()}
	}
	m.mu.Lock()
	settings := m.settings
	configured := m.configured
	lastError := m.lastError
	m.mu.Unlock()

	status := Status{
		Available:  m.mount != "",
		Configured: configured,
		Enabled:    settings.Enabled,
		Mount:      m.mount,
		Root:       m.root,
		CheckedAt:  time.Now().UTC(),
		LastError:  lastError,
	}
	if !status.Available {
		status.Reason = "cgroup v2 is not mounted; enable the cgroup2 filesystem"
	} else if err := m.validateRoot(); err != nil {
		status.Reason = err.Error()
	} else if configured {
		status.Controllers = controllersAt(m.root)
	}
	if status.Controllers == nil {
		status.Controllers = controllersAt(m.mount)
	}
	if cpus, err := OnlineCPUs(); err == nil {
		status.OnlineCPUs = cpus
	}
	status.CPUModel = CPUModel()
	return status
}

// NormalizeSettings trims CPU, memory-node, and device values and canonicalizes
// device paths.
func NormalizeSettings(settings Settings) Settings {
	return normalizeSettings(settings)
}

// ValidateSettings checks host-wide settings without touching the cgroup
// hierarchy. Disabled host limits are not applied, so host fields are not
// validated, but class definitions are still checked and can be prepared before
// enabling them.
func ValidateSettings(settings Settings) error {
	settings = normalizeSettings(settings)
	if settings.Enabled {
		return validateSettings(settings)
	}
	return validateClasses(settings.Classes)
}

// Validate checks settings without touching the cgroup hierarchy.
func (m *Manager) Validate(settings Settings) error {
	if settings.Enabled {
		return validateSettings(settings)
	}
	return validateClasses(settings.Classes)
}

// Configure validates and applies host-wide settings. The desired settings
// are retained even when applying fails so a later VM start can retry after
// the host cgroup state is repaired.
func (m *Manager) Configure(settings Settings) error {
	if m == nil {
		return errors.New("cgroup manager is not configured")
	}
	settings = normalizeSettings(settings)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = cloneSettings(settings)
	m.configured = false
	if !settings.Enabled {
		if err := m.disableLocked(); err != nil {
			m.lastError = err.Error()
			return err
		}
		m.lastError = ""
		return nil
	}
	if err := validateSettings(settings); err != nil {
		m.lastError = err.Error()
		return err
	}
	if err := m.ensureLocked(); err != nil {
		m.lastError = err.Error()
		return err
	}
	m.configured = true
	m.lastError = ""
	return nil
}

// Place ensures the shared or named class cgroup and returns where QEMU
// should be launched. When host limits are disabled it returns an empty
// Placement. An empty className selects the shared cgroup.
func (m *Manager) Place(className string) (Placement, error) {
	if m == nil {
		return Placement{}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.settings.Enabled {
		return Placement{}, nil
	}
	if err := validateSettings(m.settings); err != nil {
		m.lastError = err.Error()
		return Placement{}, err
	}
	if err := m.ensureLocked(); err != nil {
		m.lastError = err.Error()
		return Placement{}, err
	}
	m.configured = true
	m.lastError = ""

	name := strings.TrimSpace(className)
	var dir string
	if name == "" {
		dir = filepath.Join(m.root, sharedName)
	} else {
		if !m.classExistsLocked(name) {
			return Placement{}, fmt.Errorf("cgroup class %q not found", name)
		}
		if m.classHasChildrenLocked(name) {
			return Placement{}, fmt.Errorf("cgroup class %q has child classes; assign the VM to a leaf class", name)
		}
		dir = filepath.Join(m.root, filepath.FromSlash(name))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Placement{}, fmt.Errorf("create cgroup %s: %w", dir, err)
	}
	return Placement{Path: dir, Enabled: true}, nil
}

// Assign moves pid into the placement's cgroup. It is a no-op for disabled
// placements.
func (m *Manager) Assign(placement Placement, pid int) error {
	if !placement.Enabled || strings.TrimSpace(placement.Path) == "" || pid < 1 {
		return nil
	}
	return writeCgroup(placement.Path, procsFile, strconv.Itoa(pid), true)
}

// Release moves a process out of the Rookery cgroup hierarchy so it is no
// longer constrained. It is used when a VM is unassigned while host-wide
// limits are off.
func (m *Manager) Release(pid int) error {
	if m == nil || pid < 1 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mount == "" || m.validateRoot() != nil {
		return nil
	}
	if !m.pidInHierarchyLocked(pid) {
		return nil
	}
	return m.movePIDOutLocked(pid)
}

// disableLocked moves every process out of the Rookery hierarchy and removes
// the child cgroups and the parent. It is called when host-wide cgroup limits
// are disabled so running VMs stop being constrained immediately.
func (m *Manager) disableLocked() error {
	if m.mount == "" || m.validateRoot() != nil {
		return nil
	}
	if _, err := os.Stat(m.root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect cgroup root %s: %w", m.root, err)
	}
	dirs := collectCgroupDirs(m.root)
	self := strconv.Itoa(os.Getpid())
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, procsFile))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read %s/%s: %w", dir, procsFile, err)
		}
		for _, pid := range strings.Fields(string(data)) {
			pidNumber, err := strconv.Atoi(pid)
			if err != nil || pidNumber < 1 {
				continue
			}
			if pid == self || !looksLikeQEMU(pidNumber) {
				// Never sweep unrelated host services into the cgroup root if
				// the configured root was pointed at a shared hierarchy.
				continue
			}
			if err := m.writePIDOutLocked(pid); err != nil {
				return err
			}
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove cgroup %s: %w", dirs[i], err)
		}
	}
	return nil
}

func collectCgroupDirs(root string) []string {
	dirs := make([]string, 0, 4)
	var walk func(string)
	walk = func(dir string) {
		dirs = append(dirs, dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				walk(filepath.Join(dir, entry.Name()))
			}
		}
	}
	walk(root)
	return dirs
}

func (m *Manager) movePIDOutLocked(pid int) error {
	return m.writePIDOutLocked(strconv.Itoa(pid))
}

func (m *Manager) writePIDOutLocked(pid string) error {
	target := filepath.Join(m.mount, procsFile)
	if err := os.WriteFile(target, []byte(pid), 0o644); err != nil {
		return fmt.Errorf("move pid %s out of Rookery cgroup: %w", pid, err)
	}
	return nil
}

func (m *Manager) pidInHierarchyLocked(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(m.mount, m.root)
	if err != nil {
		return false
	}
	prefix := strings.TrimPrefix(filepath.Clean(relative), string(filepath.Separator))
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(fields) != 3 {
			continue
		}
		path := strings.TrimPrefix(fields[2], "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (m *Manager) validateRoot() error {
	if !filepath.IsAbs(m.root) {
		return fmt.Errorf("cgroup root %s must be an absolute path", m.root)
	}
	relative, err := filepath.Rel(m.mount, m.root)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("cgroup root %s must be a child of the cgroup2 mount %s", m.root, m.mount)
	}
	return nil
}

func (m *Manager) ensureLocked() error {
	if m.mount == "" {
		return errors.New("cgroup v2 is not mounted; enable the cgroup2 filesystem")
	}
	if err := m.validateRoot(); err != nil {
		return err
	}
	parent := filepath.Dir(m.root)
	// Enable the controllers in the parent so the Rookery cgroup exposes
	// them. Failures are tolerated here: applyLimits reports a precise error
	// only when a limit actually needs a missing controller.
	_ = enableControllers(parent, managedControllers)
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return fmt.Errorf("create cgroup root %s: %w", m.root, err)
	}
	shared := filepath.Join(m.root, sharedName)
	if err := os.MkdirAll(shared, 0o755); err != nil {
		return fmt.Errorf("create shared cgroup %s: %w", shared, err)
	}
	// A root cgroup with processes cannot distribute domain controllers to
	// children (cgroup v2's no-internal-process rule). Older versions or a
	// manual move can leave QEMU there, so relocate those processes first.
	// Only QEMU-like processes are moved so a misconfigured root cannot sweep
	// unrelated host services into the shared cgroup.
	moveQEMUProcesses(m.root, shared)
	_ = enableControllers(m.root, managedControllers)

	effective, err := m.effectiveLimitsLocked("")
	if err != nil {
		return err
	}
	// Shrinking the parent cpuset while a child still references removed CPUs
	// fails with EBUSY. Clamp existing children first; class limits are
	// restored from settings on the next reconciliation.
	if err := clampChildCPUs(m.root, effective.CPUs, effective.Mems); err != nil {
		return err
	}
	if err := applyLimits(m.root, effective); err != nil {
		return err
	}
	if err := applyLimits(shared, effective); err != nil {
		return err
	}
	if err := m.ensureClassesLocked(); err != nil {
		return err
	}
	return nil
}

func clampChildCPUs(dir, allowedCPUs, allowedMems string) error {
	allowed, err := ParseCPUSet(allowedCPUs)
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		return nil
	}
	mems, err := ParseCPUSet(allowedMems)
	if err != nil {
		return err
	}
	entries := collectCgroupDirs(dir)
	for _, child := range entries[1:] {
		if err := clampCgroupCPUSet(filepath.Join(child, "cpuset.cpus"), allowed); err != nil {
			return err
		}
		if len(mems) > 0 {
			if err := clampCgroupCPUSet(filepath.Join(child, "cpuset.mems"), mems); err != nil {
				return err
			}
		}
	}
	return nil
}

func clampCgroupCPUSet(path string, allowed []int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	current, err := ParseCPUSet(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(current) == 0 {
		return nil
	}
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	clamped := make([]int, 0, len(current))
	for _, value := range current {
		if _, ok := allowedSet[value]; ok {
			clamped = append(clamped, value)
		}
	}
	if len(clamped) == 0 {
		clamped = []int{allowed[0]}
	}
	value := FormatCPUSet(clamped)
	if value == FormatCPUSet(current) {
		return nil
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// moveQEMUProcesses moves QEMU processes from one cgroup to another, skipping
// the Rookery process itself.
func moveQEMUProcesses(from, to string) {
	data, err := os.ReadFile(filepath.Join(from, procsFile))
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, field := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid < 1 || pid == self || !looksLikeQEMU(pid) {
			continue
		}
		_ = os.WriteFile(filepath.Join(to, procsFile), []byte(strconv.Itoa(pid)), 0o644)
	}
}

func looksLikeQEMU(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	args := string(data)
	return strings.Contains(args, "qemu-system") || strings.Contains(args, "-uuid")
}

type appliedLimits struct {
	CPUs           string
	Mems           string
	MemoryMaxBytes int64
	CPUWeight      int
	IOLimits       []IOLimit
}

// ensureClassesLocked creates class nodes directly below the Rookery root and
// applies their effective limits. A class with child classes is a grouping node
// with controllers enabled and no processes; VMs are only assigned to leaf
// classes.
func (m *Manager) ensureClassesLocked() error {
	defined := classMap(m.settings.Classes)
	nodeSet := make(map[string]struct{})
	for name := range defined {
		for _, prefix := range classPrefixes(name) {
			nodeSet[prefix] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for name := range nodeSet {
		nodes = append(nodes, name)
	}
	sort.Slice(nodes, func(i, j int) bool {
		di, dj := strings.Count(nodes[i], "/"), strings.Count(nodes[j], "/")
		if di != dj {
			return di < dj
		}
		return nodes[i] < nodes[j]
	})

	desired := map[string]struct{}{
		m.root:                            {},
		filepath.Join(m.root, sharedName): {},
	}
	for _, name := range nodes {
		dir := filepath.Join(m.root, filepath.FromSlash(name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create cgroup class %s: %w", name, err)
		}
		desired[dir] = struct{}{}
		if classHasChildren(name, defined) {
			_ = enableControllers(dir, managedControllers)
		}
		effective, err := m.effectiveLimitsLocked(name)
		if err != nil {
			return err
		}
		if err := applyLimits(dir, effective); err != nil {
			return err
		}
	}

	dirs := collectCgroupDirs(m.root)
	for i := len(dirs) - 1; i >= 0; i-- {
		if dirs[i] == m.root {
			continue
		}
		if _, ok := desired[dirs[i]]; ok {
			continue
		}
		if err := os.Remove(dirs[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale cgroup %s: %w", dirs[i], err)
		}
	}
	return nil
}

func classHasChildren(name string, classes map[string]Class) bool {
	prefix := name + "/"
	for other := range classes {
		if strings.HasPrefix(other, prefix) {
			return true
		}
	}
	return false
}

func (m *Manager) classHasChildrenLocked(name string) bool {
	return classHasChildren(name, classMap(m.settings.Classes))
}

func (m *Manager) effectiveLimitsLocked(className string) (appliedLimits, error) {
	host := m.settings
	online, err := OnlineCPUs()
	if err != nil {
		return appliedLimits{}, err
	}
	hostCPUs := host.CPUs
	if hostCPUs != "" {
		if _, err := ValidateCPUSet(hostCPUs, online); err != nil {
			return appliedLimits{}, err
		}
	}
	effectiveCPUs := hostCPUs
	if effectiveCPUs == "" {
		// Always write an explicit cpuset so clearing a previous restriction
		// actually resets the cgroup instead of leaving the old CPU list.
		effectiveCPUs = FormatCPUSet(online)
	}
	mems := host.Mems
	if mems == "" {
		nodes, err := OnlineMems()
		if err != nil {
			return appliedLimits{}, err
		}
		mems = FormatCPUSet(nodes)
	}

	memory := host.MemoryMaxBytes
	weight := host.CPUWeight
	ioLimits := host.IOLimits
	className = strings.TrimSpace(className)
	if className != "" {
		classes := classMap(host.Classes)
		for _, prefix := range classPrefixes(className) {
			class, ok := classes[prefix]
			if !ok {
				continue
			}
			if class.CPUs != "" {
				allowed, err := ParseCPUSet(effectiveCPUs)
				if err != nil {
					return appliedLimits{}, err
				}
				canonical, err := ValidateCPUSet(class.CPUs, allowed)
				if err != nil {
					return appliedLimits{}, fmt.Errorf("cgroup class %s CPU set: %w", prefix, err)
				}
				effectiveCPUs = canonical
			}
			if class.MemoryMaxBytes > 0 {
				memory = class.MemoryMaxBytes
			}
			if class.CPUWeight > 0 {
				weight = class.CPUWeight
			}
			ioLimits = mergeIOLimits(ioLimits, class.IOLimits)
		}
	}
	return appliedLimits{
		CPUs: effectiveCPUs, Mems: mems,
		MemoryMaxBytes: memory, CPUWeight: weight,
		IOLimits: ioLimits,
	}, nil
}

func (m *Manager) classExistsLocked(name string) bool {
	name = strings.TrimSpace(name)
	for _, class := range m.settings.Classes {
		if strings.TrimSpace(class.Name) == name {
			return true
		}
	}
	return false
}

func classMap(classes []Class) map[string]Class {
	result := make(map[string]Class, len(classes))
	for _, class := range classes {
		name := strings.TrimSpace(class.Name)
		if name != "" {
			result[name] = class
		}
	}
	return result
}

// classPrefixes returns "prod", "prod/web" for "prod/web".
func classPrefixes(name string) []string {
	segments := strings.Split(strings.Trim(name, "/"), "/")
	result := make([]string, 0, len(segments))
	current := ""
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if current == "" {
			current = segment
		} else {
			current += "/" + segment
		}
		result = append(result, current)
	}
	return result
}

func mergeIOLimits(host, vm []IOLimit) []IOLimit {
	type entry struct {
		limit IOLimit
		order int
	}
	index := make(map[string]*entry)
	order := make([]string, 0, len(host)+len(vm))
	add := func(limit IOLimit) {
		key := filepath.Clean(strings.TrimSpace(limit.Device))
		item, ok := index[key]
		if !ok {
			item = &entry{limit: limit, order: len(order)}
			index[key] = item
			order = append(order, key)
			return
		}
		if limit.ReadBPS > 0 {
			item.limit.ReadBPS = limit.ReadBPS
		}
		if limit.WriteBPS > 0 {
			item.limit.WriteBPS = limit.WriteBPS
		}
		if limit.ReadIOPS > 0 {
			item.limit.ReadIOPS = limit.ReadIOPS
		}
		if limit.WriteIOPS > 0 {
			item.limit.WriteIOPS = limit.WriteIOPS
		}
		if limit.Weight > 0 {
			item.limit.Weight = limit.Weight
		}
	}
	for _, limit := range host {
		add(limit)
	}
	for _, limit := range vm {
		add(limit)
	}
	result := make([]IOLimit, 0, len(order))
	for _, key := range order {
		result = append(result, index[key].limit)
	}
	return result
}

func applyLimits(dir string, limits appliedLimits) error {
	if limits.CPUs != "" {
		if err := writeCgroup(dir, "cpuset.cpus", limits.CPUs, true); err != nil {
			return err
		}
		if limits.Mems != "" {
			if err := writeCgroup(dir, "cpuset.mems", limits.Mems, true); err != nil {
				return err
			}
		}
	}
	if limits.MemoryMaxBytes > 0 {
		if err := writeCgroup(dir, "memory.max", strconv.FormatInt(limits.MemoryMaxBytes, 10), true); err != nil {
			return err
		}
	} else if fileExists(filepath.Join(dir, "memory.max")) {
		if err := writeCgroup(dir, "memory.max", maxValue, false); err != nil {
			return err
		}
	}
	if limits.CPUWeight > 0 {
		if err := writeCgroup(dir, "cpu.weight", strconv.Itoa(limits.CPUWeight), true); err != nil {
			return err
		}
	} else if fileExists(filepath.Join(dir, "cpu.weight")) {
		_ = writeCgroup(dir, "cpu.weight", strconv.Itoa(defaultWeight), false)
	}

	if len(limits.IOLimits) > 0 {
		if err := applyIOMax(dir, limits.IOLimits); err != nil {
			return err
		}
	} else if fileExists(filepath.Join(dir, "io.max")) {
		_ = clearIOMax(dir)
	}
	if hasIOWeight(limits.IOLimits) {
		if err := applyIOWeight(dir, limits.IOLimits); err != nil {
			return err
		}
	} else if fileExists(filepath.Join(dir, "io.weight")) {
		_ = clearIOWeight(dir)
	}
	return nil
}

func applyIOMax(dir string, limits []IOLimit) error {
	resolved, order, err := resolveIOLimits(limits)
	if err != nil {
		return err
	}
	// Clear rules for devices that are no longer limited so removed settings
	// do not stay in effect.
	if err := clearIOMax(dir); err != nil {
		return err
	}
	lines := make([]string, 0, len(order))
	for _, key := range order {
		limit := resolved[key]
		line := fmt.Sprintf("%s rbps=%s wbps=%s riops=%s wiops=%s", key,
			ioValue(limit.ReadBPS), ioValue(limit.WriteBPS),
			ioValue(limit.ReadIOPS), ioValue(limit.WriteIOPS))
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}
	return writeCgroup(dir, "io.max", strings.Join(lines, "\n"), true)
}

func applyIOWeight(dir string, limits []IOLimit) error {
	resolved, order, err := resolveIOLimits(limits)
	if err != nil {
		return err
	}
	// Reset per-device weights that are no longer configured before applying
	// the new set.
	if err := clearIOWeight(dir); err != nil {
		return err
	}
	lines := []string{"default " + strconv.Itoa(defaultWeight)}
	for _, key := range order {
		weight := resolved[key].Weight
		if weight > 0 {
			lines = append(lines, key+" "+strconv.Itoa(weight))
		}
	}
	return writeCgroup(dir, "io.weight", strings.Join(lines, "\n"), true)
}

func clearIOMax(dir string) error {
	path := filepath.Join(dir, "io.max")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		lines = append(lines, fields[0]+" rbps=max wbps=max riops=max wiops=max")
	}
	if len(lines) == 0 {
		return nil
	}
	return writeCgroup(dir, "io.max", strings.Join(lines, "\n"), false)
}

func clearIOWeight(dir string) error {
	path := filepath.Join(dir, "io.weight")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "default" {
			continue
		}
		lines = append(lines, fields[0]+" "+strconv.Itoa(defaultWeight))
	}
	if len(lines) == 0 {
		return nil
	}
	return writeCgroup(dir, "io.weight", strings.Join(lines, "\n"), false)
}

func resolveIOLimits(limits []IOLimit) (map[string]IOLimit, []string, error) {
	resolved := make(map[string]IOLimit, len(limits))
	order := make([]string, 0, len(limits))
	for _, limit := range limits {
		major, minor, err := DeviceNumber(limit.Device)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve I/O device %q: %w", limit.Device, err)
		}
		key := fmt.Sprintf("%d:%d", major, minor)
		value, ok := resolved[key]
		if !ok {
			value = limit
			order = append(order, key)
		} else {
			if limit.ReadBPS > 0 {
				value.ReadBPS = limit.ReadBPS
			}
			if limit.WriteBPS > 0 {
				value.WriteBPS = limit.WriteBPS
			}
			if limit.ReadIOPS > 0 {
				value.ReadIOPS = limit.ReadIOPS
			}
			if limit.WriteIOPS > 0 {
				value.WriteIOPS = limit.WriteIOPS
			}
			if limit.Weight > 0 {
				value.Weight = limit.Weight
			}
		}
		resolved[key] = value
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i] < order[j] })
	return resolved, order, nil
}

func hasIOWeight(limits []IOLimit) bool {
	for _, limit := range limits {
		if limit.Weight > 0 {
			return true
		}
	}
	return false
}

func ioValue(value int64) string {
	if value <= 0 {
		return maxValue
	}
	return strconv.FormatInt(value, 10)
}

// DeviceNumber resolves a device path to the major/minor pair used by io.max
// and io.weight.
func DeviceNumber(path string) (uint32, uint32, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, 0, errors.New("device path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join("/dev", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("%s is not a device", path)
	}
	device := uint64(stat.Rdev)
	if device == 0 {
		return 0, 0, fmt.Errorf("%s is not a block device", path)
	}
	return unix.Major(device), unix.Minor(device), nil
}

func validateSettings(settings Settings) error {
	if settings.MemoryMaxBytes < 0 {
		return errors.New("memory_max_bytes must not be negative")
	}
	if settings.CPUWeight < 0 || settings.CPUWeight > 10000 {
		return errors.New("cpu_weight must be between 1 and 10000 (0 means inherit)")
	}
	online, err := OnlineCPUs()
	if err != nil {
		return err
	}
	if _, err := ValidateCPUSet(settings.CPUs, online); err != nil {
		return err
	}
	mems, err := OnlineMems()
	if err != nil {
		return err
	}
	if _, err := ValidateMems(settings.Mems, mems); err != nil {
		return err
	}
	if err := validateIOLimits(settings.IOLimits); err != nil {
		return err
	}
	if err := validateClasses(settings.Classes); err != nil {
		return err
	}
	return validateClassCPUHierarchy(settings, online)
}

func validateClassCPUHierarchy(settings Settings, online []int) error {
	hostCPUs := settings.CPUs
	if hostCPUs == "" {
		hostCPUs = FormatCPUSet(online)
	}
	classes := classMap(settings.Classes)
	cache := make(map[string]string, len(classes))
	var effective func(string) (string, error)
	effective = func(name string) (string, error) {
		if cpus, ok := cache[name]; ok {
			return cpus, nil
		}
		allowed := hostCPUs
		if parent := parentClassName(name); parent != "" {
			var err error
			allowed, err = effective(parent)
			if err != nil {
				return "", err
			}
		}
		class, ok := classes[name]
		if !ok || strings.TrimSpace(class.CPUs) == "" {
			cache[name] = allowed
			return allowed, nil
		}
		allowedSet, err := ParseCPUSet(allowed)
		if err != nil {
			return "", err
		}
		canonical, err := ValidateCPUSet(class.CPUs, allowedSet)
		if err != nil {
			return "", fmt.Errorf("cgroup class %s CPU set: %w", name, err)
		}
		cache[name] = canonical
		return canonical, nil
	}
	for name := range classes {
		if _, err := effective(name); err != nil {
			return err
		}
	}
	return nil
}

func parentClassName(name string) string {
	if index := strings.LastIndex(name, "/"); index > 0 {
		return name[:index]
	}
	return ""
}

// ValidateClassName validates a slash-separated cgroup class path.
func ValidateClassName(name string) error {
	_, err := classSegments(name)
	return err
}

func classSegments(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return nil, errors.New("cgroup class name is required")
	case len(name) > 200:
		return nil, errors.New("cgroup class name must be at most 200 characters")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return nil, fmt.Errorf("cgroup class %q must not start or end with /", name)
	}
	segments := strings.Split(name, "/")
	if len(segments) > 3 {
		return nil, fmt.Errorf("cgroup class %q is nested too deeply (maximum 3 levels below the root)", name)
	}
	for position, segment := range segments {
		switch segment {
		case "":
			return nil, fmt.Errorf("cgroup class %q contains an empty path segment", name)
		case ".", "..":
			return nil, fmt.Errorf("cgroup class %q contains an invalid path segment", name)
		}
		if position == 0 && segment == sharedName {
			return nil, fmt.Errorf("cgroup class %q uses reserved top-level name %q", name, sharedName)
		}
		if len(segment) > 63 {
			return nil, fmt.Errorf("cgroup class segment %q is too long", segment)
		}
		for position, char := range segment {
			switch {
			case char >= 'a' && char <= 'z':
			case char >= 'A' && char <= 'Z':
			case char >= '0' && char <= '9':
			case char == '.', char == '_', char == '-':
				if position == 0 {
					return nil, fmt.Errorf("cgroup class segment %q must start with a letter or digit", segment)
				}
			default:
				return nil, fmt.Errorf("cgroup class segment %q contains invalid character %q", segment, char)
			}
		}
	}
	return segments, nil
}

func validateClasses(classes []Class) error {
	if len(classes) > 64 {
		return errors.New("at most 64 cgroup classes are allowed")
	}
	seen := make(map[string]struct{}, len(classes))
	for _, class := range classes {
		name := strings.TrimSpace(class.Name)
		if err := ValidateClassName(name); err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate cgroup class %q", name)
		}
		seen[name] = struct{}{}
		if class.MemoryMaxBytes < 0 {
			return fmt.Errorf("cgroup class %s memory_max_bytes must not be negative", name)
		}
		if class.CPUWeight < 0 || class.CPUWeight > 10000 {
			return fmt.Errorf("cgroup class %s cpu_weight must be between 1 and 10000 (0 means inherit)", name)
		}
		if _, err := CanonicalCPUSet(class.CPUs); err != nil {
			return fmt.Errorf("cgroup class %s: %w", name, err)
		}
		if err := validateIOLimits(class.IOLimits); err != nil {
			return fmt.Errorf("cgroup class %s: %w", name, err)
		}
	}
	for _, class := range classes {
		name := strings.TrimSpace(class.Name)
		if parent := parentClassName(name); parent != "" {
			if _, ok := seen[parent]; !ok {
				return fmt.Errorf("cgroup class %s requires parent class %q to be defined", name, parent)
			}
		}
	}
	return nil
}

func validateIOLimits(limits []IOLimit) error {
	if len(limits) > maxIOLimits {
		return fmt.Errorf("at most %d I/O limits are allowed", maxIOLimits)
	}
	seen := make(map[string]struct{}, len(limits))
	for _, limit := range limits {
		device := strings.TrimSpace(limit.Device)
		if device == "" {
			return errors.New("I/O limit device is required")
		}
		if !filepath.IsAbs(device) {
			device = filepath.Join("/dev", device)
		}
		key := filepath.Clean(device)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate I/O limit for %s", key)
		}
		seen[key] = struct{}{}
		if limit.ReadBPS < 0 || limit.WriteBPS < 0 || limit.ReadIOPS < 0 || limit.WriteIOPS < 0 {
			return fmt.Errorf("I/O limits for %s must not be negative", key)
		}
		if limit.Weight < 0 || limit.Weight > 10000 {
			return fmt.Errorf("I/O weight for %s must be between 1 and 10000 (0 means inherit)", key)
		}
	}
	return nil
}

func normalizeSettings(settings Settings) Settings {
	settings.CPUs = strings.TrimSpace(settings.CPUs)
	if cpus, err := CanonicalCPUSet(settings.CPUs); err == nil {
		settings.CPUs = cpus
	}
	settings.Mems = strings.TrimSpace(settings.Mems)
	if mems, err := CanonicalCPUSet(settings.Mems); err == nil {
		settings.Mems = mems
	}
	settings.IOLimits = normalizeIOLimits(settings.IOLimits)
	if len(settings.Classes) > 0 {
		classes := make([]Class, 0, len(settings.Classes))
		for _, class := range settings.Classes {
			class.Name = strings.TrimSpace(class.Name)
			class.CPUs = strings.TrimSpace(class.CPUs)
			if cpus, err := CanonicalCPUSet(class.CPUs); err == nil {
				class.CPUs = cpus
			}
			class.IOLimits = normalizeIOLimits(class.IOLimits)
			classes = append(classes, class)
		}
		settings.Classes = classes
	}
	return settings
}

func normalizeIOLimits(limits []IOLimit) []IOLimit {
	if len(limits) == 0 {
		return nil
	}
	out := make([]IOLimit, 0, len(limits))
	for _, limit := range limits {
		limit.Device = strings.TrimSpace(limit.Device)
		if limit.Device != "" && !filepath.IsAbs(limit.Device) {
			limit.Device = filepath.Join("/dev", limit.Device)
		}
		limit.Device = filepath.Clean(limit.Device)
		out = append(out, limit)
	}
	return out
}

func cloneSettings(settings Settings) Settings {
	settings.IOLimits = append([]IOLimit(nil), settings.IOLimits...)
	if len(settings.Classes) > 0 {
		classes := make([]Class, len(settings.Classes))
		for i, class := range settings.Classes {
			class.IOLimits = append([]IOLimit(nil), class.IOLimits...)
			classes[i] = class
		}
		settings.Classes = classes
	}
	return settings
}

func writeCgroup(dir, name, value string, required bool) error {
	path := filepath.Join(dir, name)
	current, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(current)) == strings.TrimSpace(value) {
		return nil
	}
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cgroup v2 controller for %s is not available at %s", name, dir)
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func controllersAt(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, controllerFile))
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

func enableControllers(dir string, controllers []string) error {
	availableData, err := os.ReadFile(filepath.Join(dir, controllerFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	available := make(map[string]struct{})
	for _, controller := range strings.Fields(string(availableData)) {
		available[controller] = struct{}{}
	}
	enabled := make(map[string]struct{})
	if data, err := os.ReadFile(filepath.Join(dir, subtreeFile)); err == nil {
		for _, controller := range strings.Fields(string(data)) {
			enabled[strings.TrimPrefix(controller, "+")] = struct{}{}
		}
	}
	for _, controller := range controllers {
		if _, ok := available[controller]; !ok {
			continue
		}
		if _, ok := enabled[controller]; ok {
			continue
		}
		path := filepath.Join(dir, subtreeFile)
		if err := os.WriteFile(path, []byte("+"+controller), 0o644); err != nil {
			return fmt.Errorf("enable cgroup v2 %s controller in %s: %w", controller, dir, err)
		}
	}
	return nil
}

func cgroup2Mount() (string, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "cgroup2" {
			continue
		}
		return unescapeMount(fields[1]), nil
	}
	return "", errors.New("cgroup2 is not mounted")
}

func unescapeMount(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}
