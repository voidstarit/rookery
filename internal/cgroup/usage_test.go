package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsageReadsHierarchy(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "rookery-cgroup")
	nodes := map[string]string{
		"":         "1024\n",
		"shared":   "2048\n",
		"prod":     "3072\n",
		"prod/web": "4096\n",
	}
	for path, memory := range nodes {
		dir := root
		if path != "" {
			dir = filepath.Join(root, filepath.FromSlash(path))
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write := func(name, value string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("memory.current", memory)
		write("memory.max", "8192\n")
		write("pids.current", "2\n")
		write("pids.max", "max\n")
		write("cpuset.cpus.effective", "0-3\n")
		write("cpu.stat", "usage_usec 123456\nuser_usec 100000\nsystem_usec 23456\n")
		write("io.stat", "8:0 rbytes=100 wbytes=200 rios=3 wios=4\n8:16 rbytes=50 wbytes=60 rios=1 wios=2\n")
	}
	m := &Manager{mount: mount, root: root, configured: true, settings: Settings{
		Enabled: true, Classes: []Class{{Name: "prod"}, {Name: "prod/web"}},
	}}
	usage := m.Usage()
	if !usage.Available || !usage.Configured || len(usage.Nodes) != 4 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Nodes[0].Name != "rookery-cgroup" || usage.Nodes[1].Name != "shared" {
		t.Fatalf("node names = %+v", usage.Nodes)
	}
	web := usage.Nodes[3]
	if web.Name != "prod/web" || web.MemoryCurrentBytes != 4096 || web.MemoryMaxBytes != 8192 ||
		web.CPUUsageUsec != 123456 || web.PIDsCurrent != 2 || web.PIDsMax != 0 ||
		web.IOReadBytes != 150 || web.IOWriteBytes != 260 || web.IOReadOps != 4 || web.IOWriteOps != 6 ||
		web.CPUs != "0-3" {
		t.Fatalf("web usage = %+v", web)
	}
}
