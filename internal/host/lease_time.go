package host

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// legacyCompactDuration matches the compact forms the pre-split API stored. The
// old formatter trimmed a trailing zero from the smallest duration unit, so
// 1h30m was stored as "1h3", 30m as "3", 1h10m as "1h1", and 2h20m as "2h2".
// The final digit of such a value is the tens digit of the missing unit.
var legacyCompactDuration = regexp.MustCompile(`^(?:([0-9]+)h)?(?:([0-9]+)m)?([0-9]+)$`)

// DecodeLeaseTime decodes a configured DHCP lease duration. It accepts standard
// Go duration strings such as "12h" and "1h30m" and the legacy compact forms
// written by older releases (1h3, 3, 1h1, 2h2), so a migrated row that still
// holds the old text keeps its original meaning. A blank value or a
// non-positive duration is an error; callers apply their own default.
func DecodeLeaseTime(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("lease time is empty")
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration <= 0 {
			return 0, fmt.Errorf("invalid DHCP lease time %q", value)
		}
		return duration, nil
	}

	match := legacyCompactDuration.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("invalid DHCP lease time %q", value)
	}
	hours, _ := strconv.ParseUint(match[1], 10, 32)
	minutes, _ := strconv.ParseUint(match[2], 10, 32)
	trailing, _ := strconv.ParseUint(match[3], 10, 32)

	var duration time.Duration
	switch {
	case match[2] != "":
		// An explicit minute part means the stripped trailing digit is the
		// tens digit of seconds; the regex only leaves that part after the
		// seconds unit was not written.
		duration = time.Duration(hours)*time.Hour +
			time.Duration(minutes)*time.Minute +
			time.Duration(trailing*10)*time.Second
	case match[1] != "":
		duration = time.Duration(hours)*time.Hour +
			time.Duration(trailing*10)*time.Minute
	default:
		duration = time.Duration(trailing*10) * time.Minute
	}
	if duration <= 0 {
		return 0, fmt.Errorf("invalid DHCP lease time %q", value)
	}
	return duration, nil
}
