package qemu

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Metrics is process-level runtime telemetry for a VM's QEMU instance.
// CPU and I/O counters are cumulative; the UI converts them to rates by
// sampling this endpoint repeatedly.
type Metrics struct {
	Running         bool    `json:"running"`
	PID             int     `json:"pid,omitempty"`
	UptimeSeconds   uint64  `json:"uptime_seconds"`
	CPUSecondsTotal float64 `json:"cpu_seconds_total"`
	RSSBytes        uint64  `json:"memory_rss_bytes"`
	VirtualBytes    uint64  `json:"memory_virtual_bytes"`
	ReadBytesTotal  uint64  `json:"disk_read_bytes_total"`
	WriteBytesTotal uint64  `json:"disk_write_bytes_total"`
}

// Metrics returns runtime metrics for one VM. A stopped VM is not an error;
// Running is false and the remaining values are zero.
func (q *QEMU) Metrics(vmID string) (Metrics, error) {
	_, pidfile, _, err := q.paths(vmID)
	if err != nil {
		return Metrics{}, err
	}
	pid, ok := readPID(pidfile)
	if !ok || !processAlive(pid) || !processMatches(pid, vmID) {
		return Metrics{Running: false}, nil
	}

	result := Metrics{Running: true, PID: pid}
	if info, err := os.Stat(pidfile); err == nil {
		if uptime := time.Since(info.ModTime()); uptime > 0 {
			result.UptimeSeconds = uint64(uptime.Seconds())
		}
	}
	result.CPUSecondsTotal = processCPUSeconds(pid)
	result.RSSBytes, result.VirtualBytes = processMemory(pid)
	result.ReadBytesTotal, result.WriteBytesTotal = processIO(pid)
	return result, nil
}

func processCPUSeconds(pid int) float64 {
	tasksDir := filepath.Join("/proc", strconv.Itoa(pid), "task")
	tasks, err := os.ReadDir(tasksDir)
	if err == nil {
		var total uint64
		var found bool
		for _, task := range tasks {
			data, err := os.ReadFile(filepath.Join(tasksDir, task.Name(), "schedstat"))
			if err != nil {
				continue
			}
			fields := strings.Fields(string(data))
			if len(fields) == 0 {
				continue
			}
			ns, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil {
				continue
			}
			total += ns
			found = true
		}
		if found {
			return float64(total) / 1e9
		}
	}

	// Fallback for kernels without schedstat. USER_HZ is 100 on Linux.
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	text := string(data)
	closeParen := strings.LastIndex(text, ")")
	if closeParen < 0 {
		return 0
	}
	fields := strings.Fields(text[closeParen+1:])
	if len(fields) < 13 {
		return 0
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	return float64(utime+stime) / 100
}

func processMemory(pid int) (rss, virtual uint64) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "VmRSS:":
			rss = value * 1024
		case "VmSize:":
			virtual = value * 1024
		}
	}
	return rss, virtual
}

func processIO(pid int) (readBytes, writeBytes uint64) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "io"))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "read_bytes:":
			readBytes = value
		case "write_bytes:":
			writeBytes = value
		}
	}
	return readBytes, writeBytes
}
