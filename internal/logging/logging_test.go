package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]zerolog.Level{
		"":         zerolog.InfoLevel,
		"info":     zerolog.InfoLevel,
		"DEBUG":    zerolog.DebugLevel,
		"warn":     zerolog.WarnLevel,
		"warning":  zerolog.WarnLevel,
		"error":    zerolog.ErrorLevel,
		"trace":    zerolog.TraceLevel,
		"disabled": zerolog.Disabled,
		"off":      zerolog.Disabled,
	}
	for value, want := range cases {
		got, err := ParseLevel(value)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", value, err)
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", value, got, want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("ParseLevel accepted an unknown level")
	}
}

func TestParseFormat(t *testing.T) {
	for _, value := range []string{"", "console", "text"} {
		if got, err := ParseFormat(value); err != nil || got != "console" {
			t.Errorf("ParseFormat(%q) = %q, %v", value, got, err)
		}
	}
	if got, err := ParseFormat("JSON"); err != nil || got != "json" {
		t.Errorf("ParseFormat(JSON) = %q, %v", got, err)
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Fatal("ParseFormat accepted an unknown format")
	}
}

func TestSetupJSONHonorsLevelAndService(t *testing.T) {
	var output bytes.Buffer
	logger, err := Setup(Options{Level: "info", Format: "json", Service: "vnfs", Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug().Msg("hidden")
	logger.Info().Int64("vnf_id", 7).Msg("hello")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("output lines = %q", output.String())
	}
	for _, want := range []string{`"level":"info"`, `"service":"vnfs"`, `"vnf_id":7`, `"message":"hello"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("output %s does not contain %s", lines[0], want)
		}
	}
}

func TestSetupConsole(t *testing.T) {
	var output bytes.Buffer
	logger, err := Setup(Options{Format: "console", Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn().Str("path", "/tmp/x").Msg("careful")
	if !strings.Contains(output.String(), "careful") || !strings.Contains(output.String(), "path=/tmp/x") {
		t.Fatalf("console output = %q", output.String())
	}
}

func TestSetupRejectsBadOptions(t *testing.T) {
	if _, err := Setup(Options{Level: "loud"}); err == nil {
		t.Fatal("Setup accepted an unknown level")
	}
	if _, err := Setup(Options{Format: "xml"}); err == nil {
		t.Fatal("Setup accepted an unknown format")
	}
}
