// Package host provides local host operations used to initialize and inspect
// the VM backend: LVM thin pools and the FD.io VPP binary API.
package host

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes a command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands on the local host.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return output, fmt.Errorf("%s: %w", name, err)
		}
		return output, fmt.Errorf("%s: %w: %s", name, err, detail)
	}
	return output, nil
}
