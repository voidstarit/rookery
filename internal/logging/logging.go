// Package logging configures the process-wide zerolog logger used by the API
// server and the VNF daemon. Both binaries expose -log-level and -log-format
// flags (and NERDS_VMS_LOG_LEVEL / NERDS_VMS_LOG_FORMAT) so operators can
// switch between human-readable console output and JSON without code changes.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Options controls the global logger.
type Options struct {
	// Level is one of trace, debug, info, warn, error, fatal, panic, disabled.
	// Empty means info.
	Level string
	// Format is "console" (default) or "json".
	Format string
	// Service is added to every line, e.g. "rookery" or "vnfs".
	Service string
	// Output defaults to stdout.
	Output io.Writer
}

// ParseLevel converts a level name into a zerolog level.
func ParseLevel(value string) (zerolog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return zerolog.InfoLevel, nil
	case "trace":
		return zerolog.TraceLevel, nil
	case "debug":
		return zerolog.DebugLevel, nil
	case "warn", "warning":
		return zerolog.WarnLevel, nil
	case "error":
		return zerolog.ErrorLevel, nil
	case "fatal":
		return zerolog.FatalLevel, nil
	case "panic":
		return zerolog.PanicLevel, nil
	case "disabled", "off":
		return zerolog.Disabled, nil
	default:
		return zerolog.NoLevel, fmt.Errorf("unknown log level %q", value)
	}
}

// ParseFormat validates the output format.
func ParseFormat(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "console", "text":
		return "console", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("unknown log format %q (want console or json)", value)
	}
}

// isTerminal reports whether output is an interactive terminal. Console logs
// keep colors only there; files, pipes, and journald get plain text.
func isTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Setup configures the global zerolog logger and returns it. The logger is
// also installed as the package-level log.Logger used by internal packages.
func Setup(options Options) (zerolog.Logger, error) {
	level, err := ParseLevel(options.Level)
	if err != nil {
		return zerolog.Nop(), err
	}
	format, err := ParseFormat(options.Format)
	if err != nil {
		return zerolog.Nop(), err
	}
	output := options.Output
	if output == nil {
		output = os.Stdout
	}
	var writer io.Writer = output
	if format == "console" {
		writer = zerolog.ConsoleWriter{Out: output, TimeFormat: time.RFC3339, NoColor: !isTerminal(output)}
	}
	builder := zerolog.New(writer).Level(level).With().Timestamp()
	if service := strings.TrimSpace(options.Service); service != "" {
		builder = builder.Str("service", service)
	}
	logger := builder.Logger()
	log.Logger = logger
	return logger, nil
}
