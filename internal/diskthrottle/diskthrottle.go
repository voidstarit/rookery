// Package diskthrottle validates and formats QEMU block-throttling limits.
package diskthrottle

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// ThrottleValueMax is QEMU's THROTTLE_VALUE_MAX for block throttling.
	ThrottleValueMax int64 = 1_000_000_000_000_000
	// Uint32Max bounds QEMU's burst-length fields.
	Uint32Max int64 = 4_294_967_295
)

// Limits is the complete QEMU block-throttling policy for one disk. Zero means
// unset for every field.
type Limits struct {
	BPSTotal           int64 `json:"bps_total,omitempty"`
	BPSTotalMax        int64 `json:"bps_total_max,omitempty"`
	BPSTotalMaxLength  int64 `json:"bps_total_max_length,omitempty"`
	BPSRead            int64 `json:"bps_read,omitempty"`
	BPSReadMax         int64 `json:"bps_read_max,omitempty"`
	BPSReadMaxLength   int64 `json:"bps_read_max_length,omitempty"`
	BPSWrite           int64 `json:"bps_write,omitempty"`
	BPSWriteMax        int64 `json:"bps_write_max,omitempty"`
	BPSWriteMaxLength  int64 `json:"bps_write_max_length,omitempty"`
	IOPSTotal          int64 `json:"iops_total,omitempty"`
	IOPSTotalMax       int64 `json:"iops_total_max,omitempty"`
	IOPSTotalMaxLength int64 `json:"iops_total_max_length,omitempty"`
	IOPSRead           int64 `json:"iops_read,omitempty"`
	IOPSReadMax        int64 `json:"iops_read_max,omitempty"`
	IOPSReadMaxLength  int64 `json:"iops_read_max_length,omitempty"`
	IOPSWrite          int64 `json:"iops_write,omitempty"`
	IOPSWriteMax       int64 `json:"iops_write_max,omitempty"`
	IOPSWriteMaxLength int64 `json:"iops_write_max_length,omitempty"`
	IOPSSize           int64 `json:"iops_size,omitempty"`
}

// Validate applies Rookery's cross-field rules and QEMU's burst checks.
func (l Limits) Validate() error {
	values := []struct {
		name  string
		value int64
	}{
		{"bps_total", l.BPSTotal},
		{"bps_total_max", l.BPSTotalMax},
		{"bps_total_max_length", l.BPSTotalMaxLength},
		{"bps_read", l.BPSRead},
		{"bps_read_max", l.BPSReadMax},
		{"bps_read_max_length", l.BPSReadMaxLength},
		{"bps_write", l.BPSWrite},
		{"bps_write_max", l.BPSWriteMax},
		{"bps_write_max_length", l.BPSWriteMaxLength},
		{"iops_total", l.IOPSTotal},
		{"iops_total_max", l.IOPSTotalMax},
		{"iops_total_max_length", l.IOPSTotalMaxLength},
		{"iops_read", l.IOPSRead},
		{"iops_read_max", l.IOPSReadMax},
		{"iops_read_max_length", l.IOPSReadMaxLength},
		{"iops_write", l.IOPSWrite},
		{"iops_write_max", l.IOPSWriteMax},
		{"iops_write_max_length", l.IOPSWriteMaxLength},
		{"iops_size", l.IOPSSize},
	}
	for _, item := range values {
		if item.value < 0 {
			return fmt.Errorf("%s must not be negative", item.name)
		}
	}

	bpsBase := l.BPSTotal > 0 || l.BPSRead > 0 || l.BPSWrite > 0
	iopsBase := l.IOPSTotal > 0 || l.IOPSRead > 0 || l.IOPSWrite > 0
	if !bpsBase && !iopsBase {
		return errors.New("at least one BPS or IOPS base limit is required")
	}
	if l.IOPSSize > 0 && !iopsBase {
		return errors.New("iops_size requires a positive IOPS base")
	}

	if err := validateFamily("bps",
		l.BPSTotal, l.BPSTotalMax, l.BPSTotalMaxLength,
		l.BPSRead, l.BPSReadMax, l.BPSReadMaxLength,
		l.BPSWrite, l.BPSWriteMax, l.BPSWriteMaxLength,
	); err != nil {
		return err
	}
	if err := validateFamily("iops",
		l.IOPSTotal, l.IOPSTotalMax, l.IOPSTotalMaxLength,
		l.IOPSRead, l.IOPSReadMax, l.IOPSReadMaxLength,
		l.IOPSWrite, l.IOPSWriteMax, l.IOPSWriteMaxLength,
	); err != nil {
		return err
	}
	return nil
}

func validateFamily(name string, total, totalMax, totalLength, read, readMax, readLength, write, writeMax, writeLength int64) error {
	totalSet := total > 0
	directionalSet := read > 0 || readMax > 0 || readLength > 0 ||
		write > 0 || writeMax > 0 || writeLength > 0
	totalExtras := totalMax > 0 || totalLength > 0

	if totalSet && directionalSet {
		return fmt.Errorf("%s total and read/write limits cannot be combined", name)
	}
	if totalSet {
		return validateBurst(name+" total", total, totalMax, totalLength)
	}
	if totalExtras {
		return fmt.Errorf("%s total burst requires a positive total base", name)
	}
	if err := validateBurst(name+" read", read, readMax, readLength); err != nil {
		return err
	}
	if err := validateBurst(name+" write", write, writeMax, writeLength); err != nil {
		return err
	}
	return nil
}

func validateBurst(name string, base, max, length int64) error {
	if base > ThrottleValueMax {
		return fmt.Errorf("%s must not exceed %d", name, ThrottleValueMax)
	}
	if max > ThrottleValueMax {
		return fmt.Errorf("%s max must not exceed %d", name, ThrottleValueMax)
	}
	if max > 0 && base == 0 {
		return fmt.Errorf("%s max requires a positive base", name)
	}
	if length > 0 && max == 0 {
		return fmt.Errorf("%s max length requires a max", name)
	}
	if max > 0 && max < base {
		return fmt.Errorf("%s max must be at least its base", name)
	}
	if length > Uint32Max {
		return fmt.Errorf("%s max length must not exceed %d", name, Uint32Max)
	}
	if length > 0 && max > 0 && length > ThrottleValueMax/max {
		return fmt.Errorf("%s max length exceeds QEMU's burst limit", name)
	}
	return nil
}

// QEMUOptions returns a comma-prefixed throttling fragment, or an empty string.
func (l Limits) QEMUOptions() string {
	parts := make([]string, 0, 19)
	add := func(name string, value int64) {
		if value > 0 {
			parts = append(parts, fmt.Sprintf("throttling.%s=%d", name, value))
		}
	}
	add("bps-total", l.BPSTotal)
	add("bps-total-max", l.BPSTotalMax)
	add("bps-total-max-length", l.BPSTotalMaxLength)
	add("bps-read", l.BPSRead)
	add("bps-read-max", l.BPSReadMax)
	add("bps-read-max-length", l.BPSReadMaxLength)
	add("bps-write", l.BPSWrite)
	add("bps-write-max", l.BPSWriteMax)
	add("bps-write-max-length", l.BPSWriteMaxLength)
	add("iops-total", l.IOPSTotal)
	add("iops-total-max", l.IOPSTotalMax)
	add("iops-total-max-length", l.IOPSTotalMaxLength)
	add("iops-read", l.IOPSRead)
	add("iops-read-max", l.IOPSReadMax)
	add("iops-read-max-length", l.IOPSReadMaxLength)
	add("iops-write", l.IOPSWrite)
	add("iops-write-max", l.IOPSWriteMax)
	add("iops-write-max-length", l.IOPSWriteMaxLength)
	add("iops-size", l.IOPSSize)
	if len(parts) == 0 {
		return ""
	}
	return "," + strings.Join(parts, ",")
}
