package api

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	startupScriptTimeout     = 5 * time.Minute
	startupScriptOutputLimit = 64 * 1024
)

// runRookeryStartupScriptOnce executes the Rookery startup script once for
// this API process. It is not rerun when VPP restarts.
func (s *Server) runRookeryStartupScriptOnce() {
	s.rookeryStartupOnce.Do(func() {
		s.runRookeryStartupScript()
	})
}

func (s *Server) runRookeryStartupScript() {
	if s.store == nil {
		return
	}
	script, err := s.store.GetStartupScript(context.Background())
	if err != nil {
		s.logger.Error().Err(err).Msg("could not load Rookery startup script")
		return
	}
	if !script.Enabled || strings.TrimSpace(script.Script) == "" {
		return
	}
	s.executeStartupScript("Rookery", script.Script, func(status string, exitCode int, output string, ranAt time.Time) error {
		_, err := s.store.SetStartupScriptResult(context.Background(), status, exitCode, output, ranAt)
		return err
	})
}

// runVPPStartupScript executes the VPP startup script every time the API
// establishes a VPP connection, including reconnects after the connection
// drops or VPP restarts.
func (s *Server) runVPPStartupScript() {
	if s.store == nil {
		return
	}
	s.vppStartupMu.Lock()
	defer s.vppStartupMu.Unlock()

	script, err := s.store.GetVPPStartupScript(context.Background())
	if err != nil {
		s.logger.Error().Err(err).Msg("could not load VPP startup script")
		return
	}
	if !script.Enabled || strings.TrimSpace(script.Script) == "" {
		return
	}
	s.executeStartupScript("VPP", script.Script, func(status string, exitCode int, output string, ranAt time.Time) error {
		_, err := s.store.SetVPPStartupScriptResult(context.Background(), status, exitCode, output, ranAt)
		return err
	})
}

func (s *Server) executeStartupScript(name, script string, update func(status string, exitCode int, output string, ranAt time.Time) error) {
	started := time.Now().UTC()
	if err := update("running", -1, "", started); err != nil {
		s.logger.Warn().Err(err).Str("script", name).Msg("could not mark startup script running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupScriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	// Do not let a child process that inherited stdout/stderr keep the runner
	// waiting indefinitely after the timeout cancels the shell.
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "ROOKERY_STARTUP_SCRIPT=1")
	if s.vppSocket != "" {
		cmd.Env = append(cmd.Env, "ROOKERY_VPP_SOCKET="+s.vppSocket)
	}
	output := &limitedBuffer{limit: startupScriptOutputLimit}
	cmd.Stdout = output
	cmd.Stderr = output

	err := cmd.Run()
	exitCode := 0
	status := "success"
	if err != nil {
		status = "failed"
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			output.WriteString("\n[startup script timed out after " + startupScriptTimeout.String() + "]")
		} else {
			output.WriteString("\n[" + err.Error() + "]")
		}
	}
	if updateErr := update(status, exitCode, output.String(), started); updateErr != nil {
		s.logger.Error().Err(updateErr).Str("script", name).Msg("could not store startup script result")
	}
	event := s.logger.Info()
	if status != "success" {
		event = s.logger.Warn()
	}
	event.Str("script", name).Str("status", status).Int("exit_code", exitCode).
		Dur("duration", time.Since(started)).Msg("startup script finished")
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) WriteString(value string) {
	_, _ = b.Write([]byte(value))
}

func (b *limitedBuffer) String() string {
	value := b.buf.String()
	if b.truncated {
		value += "\n[startup script output truncated]"
	}
	return value
}
