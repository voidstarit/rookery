package cgroup

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// ParseCPUSet parses a Linux cpuset list such as "0-3,7" into ascending CPU
// numbers. An empty value returns an empty list.
func ParseCPUSet(value string) ([]int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	seen := make(map[int]struct{})
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid CPU set %q", value)
		}
		startText, endText, hasRange := strings.Cut(part, "-")
		start, err := strconv.Atoi(strings.TrimSpace(startText))
		if err != nil || start < 0 {
			return nil, fmt.Errorf("invalid CPU set %q", value)
		}
		end := start
		if hasRange {
			end, err = strconv.Atoi(strings.TrimSpace(endText))
			if err != nil || end < start {
				return nil, fmt.Errorf("invalid CPU set %q", value)
			}
		}
		if end-start > 1<<20 {
			return nil, fmt.Errorf("CPU set %q is too large", value)
		}
		for cpu := start; cpu <= end; cpu++ {
			seen[cpu] = struct{}{}
		}
	}
	cpus := make([]int, 0, len(seen))
	for cpu := range seen {
		cpus = append(cpus, cpu)
	}
	sort.Ints(cpus)
	return cpus, nil
}

// FormatCPUSet formats CPU numbers as a canonical range list, for example
// "0-3,7".
func FormatCPUSet(cpus []int) string {
	if len(cpus) == 0 {
		return ""
	}
	unique := make(map[int]struct{}, len(cpus))
	for _, cpu := range cpus {
		if cpu >= 0 {
			unique[cpu] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return ""
	}
	sorted := make([]int, 0, len(unique))
	for cpu := range unique {
		sorted = append(sorted, cpu)
	}
	sort.Ints(sorted)

	var builder strings.Builder
	for i := 0; i < len(sorted); {
		start := sorted[i]
		end := start
		for i+1 < len(sorted) && sorted[i+1] == end+1 {
			i++
			end = sorted[i]
		}
		if builder.Len() > 0 {
			builder.WriteByte(',')
		}
		if start == end {
			builder.WriteString(strconv.Itoa(start))
		} else {
			builder.WriteString(strconv.Itoa(start))
			builder.WriteByte('-')
			builder.WriteString(strconv.Itoa(end))
		}
		i++
	}
	return builder.String()
}

// CanonicalCPUSet validates and canonicalizes a cpuset value.
func CanonicalCPUSet(value string) (string, error) {
	cpus, err := ParseCPUSet(value)
	if err != nil {
		return "", err
	}
	return FormatCPUSet(cpus), nil
}

// ValidateCPUSet validates that every CPU in value is present in allowed and
// returns the canonical form. An empty value means "all allowed CPUs".
func ValidateCPUSet(value string, allowed []int) (string, error) {
	cpus, err := ParseCPUSet(value)
	if err != nil {
		return "", err
	}
	if len(cpus) == 0 {
		return "", nil
	}
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, cpu := range allowed {
		allowedSet[cpu] = struct{}{}
	}
	for _, cpu := range cpus {
		if _, ok := allowedSet[cpu]; !ok {
			return "", fmt.Errorf("CPU %d is not available (available: %s)", cpu, FormatCPUSet(allowed))
		}
	}
	return FormatCPUSet(cpus), nil
}

// OnlineCPUs returns the CPU numbers listed in /sys/devices/system/cpu/online.
func OnlineCPUs() ([]int, error) {
	if cpus, err := readCPUSetFile("/sys/devices/system/cpu/online"); err == nil && len(cpus) > 0 {
		return cpus, nil
	}
	cpus := make([]int, runtime.NumCPU())
	for i := range cpus {
		cpus[i] = i
	}
	if len(cpus) == 0 {
		return nil, fmt.Errorf("could not determine online CPUs")
	}
	return cpus, nil
}

// OnlineMems returns the NUMA memory node numbers listed in
// /sys/devices/system/node/online.
func OnlineMems() ([]int, error) {
	if mems, err := readCPUSetFile("/sys/devices/system/node/online"); err == nil && len(mems) > 0 {
		return mems, nil
	}
	return []int{0}, nil
}

// ValidateMems validates that every memory node in value is present in
// allowed and returns the canonical form. An empty value means "all nodes".
func ValidateMems(value string, allowed []int) (string, error) {
	mems, err := ParseCPUSet(value)
	if err != nil {
		return "", fmt.Errorf("invalid memory node set: %w", err)
	}
	if len(mems) == 0 {
		return "", nil
	}
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, node := range allowed {
		allowedSet[node] = struct{}{}
	}
	for _, node := range mems {
		if _, ok := allowedSet[node]; !ok {
			return "", fmt.Errorf("memory node %d is not available (available: %s)", node, FormatCPUSet(allowed))
		}
	}
	return FormatCPUSet(mems), nil
}

// CPUModel returns the first "model name" from /proc/cpuinfo.
func CPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func readCPUSetFile(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseCPUSet(strings.TrimSpace(string(data)))
}
