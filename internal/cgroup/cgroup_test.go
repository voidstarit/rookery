package cgroup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndFormatCPUSet(t *testing.T) {
	for value, want := range map[string]string{
		"0": "0", "0-3": "0-3", "0-3,7": "0-3,7", "7,0-3": "0-3,7", "0,1,2,3,7": "0-3,7",
	} {
		cpus, err := ParseCPUSet(value)
		if err != nil {
			t.Fatalf("ParseCPUSet(%q): %v", value, err)
		}
		if got := FormatCPUSet(cpus); got != want {
			t.Fatalf("FormatCPUSet(ParseCPUSet(%q)) = %q, want %q", value, got, want)
		}
	}
	for _, value := range []string{"", "0-", "-1", "3-1", "a", "1,,2"} {
		if value == "" {
			continue
		}
		if _, err := ParseCPUSet(value); err == nil {
			t.Fatalf("ParseCPUSet(%q) = nil error, want failure", value)
		}
	}
}

func TestValidateCPUSet(t *testing.T) {
	got, err := ValidateCPUSet("1,3-4", []int{0, 1, 2, 3, 4})
	if err != nil {
		t.Fatalf("ValidateCPUSet: %v", err)
	}
	if got != "1,3-4" {
		t.Fatalf("canonical CPU set = %q, want 1,3-4", got)
	}
	if _, err := ValidateCPUSet("5", []int{0, 1}); err == nil {
		t.Fatal("ValidateCPUSet accepted an unavailable CPU")
	}
	if got, err := ValidateCPUSet("", []int{0, 1}); err != nil || got != "" {
		t.Fatalf("empty CPU set = %q, %v; want empty, nil", got, err)
	}
}

func TestMergeIOLimits(t *testing.T) {
	host := []IOLimit{
		{Device: "/dev/sda", ReadBPS: 100, WriteBPS: 200, Weight: 100},
		{Device: "/dev/sdb", ReadBPS: 300},
	}
	vm := []IOLimit{
		{Device: "/dev/sda", WriteBPS: 400, Weight: 500},
		{Device: "/dev/sdc", ReadIOPS: 50},
	}
	merged := mergeIOLimits(host, vm)
	if len(merged) != 3 {
		t.Fatalf("merged limits = %+v", merged)
	}
	if merged[0].Device != "/dev/sda" || merged[0].ReadBPS != 100 || merged[0].WriteBPS != 400 || merged[0].Weight != 500 {
		t.Fatalf("merged sda = %+v", merged[0])
	}
	if merged[1].Device != "/dev/sdb" || merged[1].ReadBPS != 300 {
		t.Fatalf("merged sdb = %+v", merged[1])
	}
	if merged[2].Device != "/dev/sdc" || merged[2].ReadIOPS != 50 {
		t.Fatalf("merged sdc = %+v", merged[2])
	}
}

func TestClampChildCPUs(t *testing.T) {
	root := t.TempDir()
	names := []string{sharedName, filepath.Join(classesDir, "prod"), filepath.Join(classesDir, "prod", membersDir)}
	for _, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cpuset.cpus"), []byte("0-7"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cpuset.mems"), []byte("0-1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := clampChildCPUs(root, "0-3", "0"); err != nil {
		t.Fatalf("clampChildCPUs: %v", err)
	}
	for _, name := range names {
		for file, want := range map[string]string{"cpuset.cpus": "0-3", "cpuset.mems": "0"} {
			data, err := os.ReadFile(filepath.Join(root, name, file))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(data)); got != want {
				t.Fatalf("%s/%s = %q, want %q", name, file, got, want)
			}
		}
	}
}

func TestConfigureDisabledRemovesHierarchy(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "rookery")
	if err := os.MkdirAll(filepath.Join(root, sharedName), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := New("")
	manager.mount = mount
	manager.root = root
	if err := manager.Configure(Settings{Enabled: false}); err != nil {
		t.Fatalf("Configure disabled: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("cgroup hierarchy still exists after disable: %v", err)
	}
}

func TestClassEffectiveLimits(t *testing.T) {
	manager := New("")
	manager.settings = Settings{
		Enabled: true, CPUs: "0-7", MemoryMaxBytes: 8 << 30, CPUWeight: 100,
		IOLimits: []IOLimit{{Device: "/dev/sda", ReadBPS: 1000}},
		Classes: []Class{
			{Name: "prod", CPUs: "0-3", MemoryMaxBytes: 4 << 30, CPUWeight: 300},
			{Name: "prod/web", CPUs: "0-1", MemoryMaxBytes: 2 << 30,
				IOLimits: []IOLimit{{Device: "/dev/sda", WriteBPS: 2000}}},
		},
	}
	effective, err := manager.effectiveLimitsLocked("prod/web")
	if err != nil {
		t.Fatalf("effectiveLimitsLocked: %v", err)
	}
	if effective.CPUs != "0-1" || effective.MemoryMaxBytes != 2<<30 || effective.CPUWeight != 300 {
		t.Fatalf("class effective limits = %+v", effective)
	}
	if len(effective.IOLimits) != 1 || effective.IOLimits[0].ReadBPS != 1000 || effective.IOLimits[0].WriteBPS != 2000 {
		t.Fatalf("class I/O limits = %+v", effective.IOLimits)
	}
}

func TestClassNames(t *testing.T) {
	for _, name := range []string{"prod", "prod/web", "prod/web-1", "prod/web/frontend"} {
		if err := ValidateClassName(name); err != nil {
			t.Fatalf("ValidateClassName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "/prod", "prod/", "prod//web", "prod/../web", "prod/members", "-prod", "prod/web space", "a/b/c/d"} {
		if err := ValidateClassName(name); err == nil {
			t.Fatalf("ValidateClassName(%q) = nil error, want failure", name)
		}
	}
	if err := validateClasses([]Class{{Name: "prod/web"}}); err == nil {
		t.Fatal("validateClasses accepted a class without its parent")
	}
}

func TestApplyLimitsWritesControllerFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"cpuset.cpus", "cpuset.mems", "memory.max", "cpu.weight", "io.max", "io.weight"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	limits := appliedLimits{
		CPUs: "0-1", Mems: "0", MemoryMaxBytes: 1 << 30, CPUWeight: 500,
		IOLimits: []IOLimit{{
			Device: "/dev/null", ReadBPS: 1024, WriteBPS: 2048, ReadIOPS: 10, WriteIOPS: 20, Weight: 500,
		}},
	}
	if err := applyLimits(dir, limits); err != nil {
		t.Fatalf("applyLimits: %v", err)
	}
	assertFile := func(name, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if got := strings.TrimSpace(string(data)); !strings.Contains(got, want) {
			t.Fatalf("%s = %q, want to contain %q", name, got, want)
		}
	}
	assertFile("cpuset.cpus", "0-1")
	assertFile("cpuset.mems", "0")
	assertFile("memory.max", "1073741824")
	assertFile("cpu.weight", "500")
	assertFile("io.max", "rbps=1024 wbps=2048 riops=10 wiops=20")
	assertFile("io.weight", "500")

	if err := applyLimits(dir, appliedLimits{CPUs: "0-1", Mems: "0"}); err != nil {
		t.Fatalf("clear applyLimits: %v", err)
	}
	assertFile("memory.max", "max")
	assertFile("cpu.weight", "100")
	assertFile("io.max", "rbps=max wbps=max riops=max wiops=max")
}

func TestConfigureFakeHierarchy(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "rookery")
	for _, dir := range []string{mount, root, filepath.Join(root, sharedName)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
		for _, name := range []string{
			"cgroup.controllers", "cgroup.subtree_control", "cgroup.procs",
			"cpuset.cpus", "cpuset.mems", "memory.max", "cpu.weight", "io.max", "io.weight",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatalf("create %s/%s: %v", dir, name, err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(mount, "cgroup.controllers"), []byte("cpu cpuset memory io pids"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu cpuset memory io pids"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := New("")
	manager.mount = mount
	manager.root = root
	settings := Settings{Enabled: true, CPUs: "0", Mems: "0", MemoryMaxBytes: 1 << 30, CPUWeight: 200}
	if err := manager.Configure(settings); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "memory.max"),
		filepath.Join(root, sharedName, "memory.max"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if got := strings.TrimSpace(string(data)); got != "1073741824" {
			t.Fatalf("%s = %q, want 1073741824", path, got)
		}
	}
	status := manager.Status()
	if !status.Available || !status.Configured {
		t.Fatalf("status = %+v", status)
	}
}

func TestDisabledSettingsSkipValidation(t *testing.T) {
	if err := ValidateSettings(Settings{Enabled: false, CPUs: "bogus", MemoryMaxBytes: -1}); err != nil {
		t.Fatalf("ValidateSettings disabled: %v", err)
	}
}

func TestInvalidEnabledSettingsDoNotRunUnconstrained(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "rookery"))
	if err := manager.Configure(Settings{Enabled: true, MemoryMaxBytes: -1}); err == nil {
		t.Fatal("Configure accepted a negative memory limit")
	}
	if !manager.Settings().Enabled {
		t.Fatal("manager discarded the enabled setting after a validation failure")
	}
	if _, err := manager.Place(""); err == nil {
		t.Fatal("Place succeeded with invalid enabled settings")
	}
}

func TestDisabledManagerDoesNotPlace(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "cgroup"))
	manager.mount = ""
	if err := manager.Configure(Settings{Enabled: false}); err != nil {
		t.Fatalf("Configure disabled: %v", err)
	}
	placement, err := manager.Place("prod")
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if placement.Enabled {
		t.Fatalf("placement = %+v, want disabled", placement)
	}
	if err := manager.Configure(Settings{Enabled: true}); err == nil {
		t.Fatal("Configure enabled without a cgroup2 mount = nil error, want failure")
	}
}

func TestConfigureFakeClassHierarchy(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "rookery")
	dirs := []string{
		mount, root, filepath.Join(root, sharedName),
		filepath.Join(root, classesDir),
		filepath.Join(root, classesDir, "prod"),
		filepath.Join(root, classesDir, "prod", membersDir),
		filepath.Join(root, classesDir, "prod", "web"),
		filepath.Join(root, classesDir, "prod", "web", membersDir),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{
			"cgroup.controllers", "cgroup.subtree_control", "cgroup.procs",
			"cpuset.cpus", "cpuset.mems", "memory.max", "cpu.weight", "io.max", "io.weight",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatalf("create %s/%s: %v", dir, name, err)
			}
		}
	}
	for _, dir := range []string{mount, root} {
		if err := os.WriteFile(filepath.Join(dir, "cgroup.controllers"), []byte("cpu cpuset memory io pids"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manager := New("")
	manager.mount = mount
	manager.root = root
	settings := Settings{
		Enabled: true, CPUs: "0-1", Mems: "0", MemoryMaxBytes: 8 << 30,
		Classes: []Class{
			{Name: "prod", CPUs: "0-1", MemoryMaxBytes: 4 << 30},
			{Name: "prod/web", CPUs: "0", MemoryMaxBytes: 2 << 30},
		},
	}
	if err := manager.Configure(settings); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(root, classesDir, "prod", "memory.max"):         "4294967296",
		filepath.Join(root, classesDir, "prod", "web", "memory.max"):  "2147483648",
		filepath.Join(root, classesDir, "prod", "web", "cpuset.cpus"): "0",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if got := strings.TrimSpace(string(data)); got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
	placement, err := manager.Place("prod/web")
	if err != nil {
		t.Fatalf("Place class: %v", err)
	}
	if want := filepath.Join(root, classesDir, "prod", "web", membersDir); placement.Path != want {
		t.Fatalf("placement = %q, want %q", placement.Path, want)
	}
	if _, err := manager.Place("missing"); err == nil {
		t.Fatal("Place accepted an unknown class")
	}
}

func TestValidateClassCPUHierarchy(t *testing.T) {
	online, err := OnlineCPUs()
	if err != nil || len(online) < 2 {
		t.Skip("need at least two online CPUs")
	}
	host := FormatCPUSet(online[:2])
	child := FormatCPUSet(online[:1])
	settings := Settings{
		Enabled: true, CPUs: host,
		Classes: []Class{{Name: "prod", CPUs: host}, {Name: "prod/web", CPUs: child}},
	}
	if err := validateClassCPUHierarchy(settings, online); err != nil {
		t.Fatalf("valid hierarchy: %v", err)
	}
	settings.Classes[1].CPUs = FormatCPUSet(online)
	if err := validateClassCPUHierarchy(settings, online); err == nil {
		t.Fatal("child class CPU set outside parent was accepted")
	}
}
