package cgroup

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// UsageNode is the live cgroup v2 resource usage for one hierarchy node.
type UsageNode struct {
	Path               string `json:"path"`
	Name               string `json:"name"`
	MemoryCurrentBytes int64  `json:"memory_current_bytes"`
	MemoryMaxBytes     int64  `json:"memory_max_bytes"`
	CPUUsageUsec       int64  `json:"cpu_usage_usec"`
	PIDsCurrent        int64  `json:"pids_current"`
	PIDsMax            int64  `json:"pids_max"`
	IOReadBytes        int64  `json:"io_read_bytes"`
	IOWriteBytes       int64  `json:"io_write_bytes"`
	IOReadOps          int64  `json:"io_read_ops"`
	IOWriteOps         int64  `json:"io_write_ops"`
	CPUs               string `json:"cpus"`
}

// Usage is a point-in-time snapshot of the Rookery cgroup hierarchy.
// CPU and I/O counters are cumulative; clients derive rates between samples.
type Usage struct {
	Available  bool        `json:"available"`
	Configured bool        `json:"configured"`
	Reason     string      `json:"reason,omitempty"`
	Root       string      `json:"root"`
	OnlineCPUs int         `json:"online_cpus"`
	SampledAt  time.Time   `json:"sampled_at"`
	Nodes      []UsageNode `json:"nodes"`
}

// Usage reads live memory, CPU, task, and I/O counters from the configured
// cgroup hierarchy. It returns an empty snapshot when cgroups are disabled or
// the hierarchy has not been created.
func (m *Manager) Usage() Usage {
	if m == nil {
		return Usage{Reason: "cgroup manager is not configured", SampledAt: time.Now().UTC()}
	}
	m.mu.Lock()
	settings := cloneSettings(m.settings)
	configured := m.configured
	lastError := m.lastError
	m.mu.Unlock()

	result := Usage{
		Available:  m.mount != "",
		Configured: configured,
		Root:       m.root,
		SampledAt:  time.Now().UTC(),
		Nodes:      make([]UsageNode, 0),
	}
	if online, err := OnlineCPUs(); err == nil {
		result.OnlineCPUs = len(online)
	}
	if !result.Available {
		result.Reason = "cgroup v2 is not mounted"
		return result
	}
	if !configured {
		result.Reason = "cgroup limits are disabled"
		return result
	}
	if lastError != "" {
		result.Reason = lastError
	}

	nodePaths := []string{"", sharedName}
	seen := map[string]struct{}{"": {}, sharedName: {}}
	for _, class := range settings.Classes {
		for _, prefix := range classPrefixes(class.Name) {
			if _, ok := seen[prefix]; ok {
				continue
			}
			seen[prefix] = struct{}{}
			nodePaths = append(nodePaths, prefix)
		}
	}
	sort.Slice(nodePaths, func(i, j int) bool {
		di, dj := strings.Count(nodePaths[i], "/"), strings.Count(nodePaths[j], "/")
		if di != dj {
			return di < dj
		}
		if nodePaths[i] == "" {
			return true
		}
		if nodePaths[j] == "" {
			return false
		}
		if nodePaths[i] == sharedName {
			return true
		}
		if nodePaths[j] == sharedName {
			return false
		}
		return nodePaths[i] < nodePaths[j]
	})

	for _, path := range nodePaths {
		dir := m.root
		if path != "" {
			dir = filepath.Join(m.root, filepath.FromSlash(path))
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		result.Nodes = append(result.Nodes, readUsageNode(dir, path))
	}
	if len(result.Nodes) == 0 && result.Reason == "" {
		result.Reason = "cgroup hierarchy has not been created"
	}
	return result
}

func readUsageNode(dir, path string) UsageNode {
	name := path
	if name == "" {
		name = filepath.Base(dir)
	}
	return UsageNode{
		Path:               path,
		Name:               name,
		MemoryCurrentBytes: readCgroupInt(filepath.Join(dir, "memory.current")),
		MemoryMaxBytes:     readCgroupMax(filepath.Join(dir, "memory.max")),
		CPUUsageUsec:       readCPUUsageUsec(filepath.Join(dir, "cpu.stat")),
		PIDsCurrent:        readCgroupInt(filepath.Join(dir, "pids.current")),
		PIDsMax:            readCgroupMax(filepath.Join(dir, "pids.max")),
		IOReadBytes:        readIOStat(filepath.Join(dir, "io.stat"), "rbytes"),
		IOWriteBytes:       readIOStat(filepath.Join(dir, "io.stat"), "wbytes"),
		IOReadOps:          readIOStat(filepath.Join(dir, "io.stat"), "rios"),
		IOWriteOps:         readIOStat(filepath.Join(dir, "io.stat"), "wios"),
		CPUs:               readCgroupString(filepath.Join(dir, "cpuset.cpus.effective")),
	}
}

func readCgroupString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readCgroupInt(path string) int64 {
	value := readCgroupString(path)
	if value == "" || value == maxValue {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func readCgroupMax(path string) int64 {
	return readCgroupInt(path)
}

func readCPUUsageUsec(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			value, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil && value > 0 {
				return value
			}
			return 0
		}
	}
	return 0
}

func readIOStat(path, key string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(string(data), "\n") {
		for _, field := range strings.Fields(line) {
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 || parts[0] != key {
				continue
			}
			value, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil && value > 0 {
				total += value
			}
		}
	}
	return total
}
