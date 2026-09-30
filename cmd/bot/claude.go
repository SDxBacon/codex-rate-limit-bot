package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type claudeRateWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

func parseClaudeUsage(raw json.RawMessage) (usageSnapshot, error) {
	var result struct {
		Available *bool `json:"rate_limits_available"`
		Limits    *struct {
			FiveHour *claudeRateWindow `json:"five_hour"`
			Weekly   *claudeRateWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return usageSnapshot{}, errors.New("invalid_usage_json")
	}
	if result.Available == nil || !*result.Available || result.Limits == nil {
		return usageSnapshot{}, errors.New("usage_unavailable")
	}
	convert := func(w *claudeRateWindow) (usageWindow, error) {
		if w == nil {
			return usageWindow{}, nil
		}
		window := usageWindow{UsedPercent: w.Utilization}
		if w.Utilization != nil && (math.IsNaN(*w.Utilization) || math.IsInf(*w.Utilization, 0) ||
			*w.Utilization < 0 || *w.Utilization > 100) {
			return usageWindow{}, errors.New("invalid_utilization")
		}
		if w.ResetsAt != nil {
			at, err := time.Parse(time.RFC3339Nano, *w.ResetsAt)
			if err != nil || at.Unix() <= 0 {
				return usageWindow{}, errors.New("invalid_reset_timestamp")
			}
			reset := at.Unix()
			window.ResetsAt = &reset
		}
		return window, nil
	}
	five, err := convert(result.Limits.FiveHour)
	if err != nil {
		return usageSnapshot{}, err
	}
	week, err := convert(result.Limits.Weekly)
	if err != nil {
		return usageSnapshot{}, err
	}
	if five.UsedPercent == nil && week.UsedPercent == nil {
		return usageSnapshot{}, errors.New("usage_unavailable")
	}
	return usageSnapshot{FiveHour: five, Weekly: week}, nil
}

func claudeEnvironment(home string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_") {
			continue
		}
		switch key {
		case "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR",
			"CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_EXTRA_BODY",
			"CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_HOST_CREDS_FILE",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_AUTOUPDATER", "DISABLE_TELEMETRY", "DISABLE_ERROR_REPORTING":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CLAUDE_CONFIG_DIR="+home, "DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1")
}

// The CLI may return cached usage without identifying its source. This probe
// reports a CLI observation only; it cannot supply timer evidence or freshness.
func probeClaudeUsage(ctx context.Context, binary, home string) (snapshot usageSnapshot, probeErr error) {
	home, err := filepath.Abs(home)
	if err != nil {
		return usageSnapshot{}, errors.New("claude setup: invalid_home")
	}
	// Resolve relative binary paths before changing the child's working directory.
	if strings.ContainsRune(binary, filepath.Separator) {
		binary, err = filepath.Abs(binary)
		if err != nil {
			return usageSnapshot{}, errors.New("claude setup: invalid_binary")
		}
	}
	work, err := os.MkdirTemp("", "claude-monitor-")
	if err != nil {
		return usageSnapshot{}, errors.New("claude setup: temporary_directory_unavailable")
	}
	defer os.RemoveAll(work)
	cmd := cliCommand(ctx, binary, "-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--safe-mode", "--tools", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--permission-mode", "dontAsk")
	cmd.Dir = work
	cmd.Env = claudeEnvironment(home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return usageSnapshot{}, errors.New("claude setup: stdin_unavailable")
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return usageSnapshot{}, errors.New("claude setup: stdout_unavailable")
	}
	defer stdout.Close()
	stderr := &cliStderr{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return usageSnapshot{}, claudeFailure("start", err, nil, stderr)
	}
	readCtx, stopRead := context.WithCancel(ctx)
	type event struct {
		line []byte
		err  error
	}
	events := make(chan event)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			select {
			case events <- event{line: append([]byte(nil), scanner.Bytes()...)}:
			case <-readCtx.Done(): // Drain stdout during shutdown.
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case events <- event{err: err}:
		case <-readCtx.Done():
		}
	}()
	stage := "initialize"
	defer func() {
		_ = stdin.Close()
		stopRead()
		waited := make(chan error, 1)
		go func() { <-readerDone; waited <- cmd.Wait() }()
		timer := time.NewTimer(processGrace)
		defer timer.Stop()
		var waitErr error
		select {
		case waitErr = <-waited:
		case <-timer.C:
			_ = killProcessGroup(cmd)
			_ = stdout.Close()
			waitErr = <-waited
		}
		if probeErr != nil {
			probeErr = claudeFailure(stage, probeErr, waitErr, stderr)
		}
	}()
	request := func(id, subtype string, skipBehaviors bool) (json.RawMessage, error) {
		fields := map[string]any{"subtype": subtype}
		if skipBehaviors {
			fields["skip_behaviors"] = true
		}
		frame := map[string]any{"type": "control_request", "request_id": id, "request": fields}
		if err := json.NewEncoder(stdin).Encode(frame); err != nil {
			return nil, errors.New("stdin_write_failed")
		}
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case e := <-events:
				if e.err != nil {
					return nil, e.err
				}
				var frame struct {
					Type     string          `json:"type"`
					Response json.RawMessage `json:"response"`
				}
				if err := json.Unmarshal(e.line, &frame); err != nil {
					return nil, errors.New("invalid_control_json")
				}
				if frame.Type != "control_response" {
					continue
				}
				var response struct {
					Subtype   string          `json:"subtype"`
					RequestID string          `json:"request_id"`
					Response  json.RawMessage `json:"response"`
					Error     json.RawMessage `json:"error"`
				}
				if err := json.Unmarshal(frame.Response, &response); err != nil {
					return nil, errors.New("invalid_control_json")
				}
				if response.RequestID != id {
					continue
				}
				if response.Subtype != "success" {
					return nil, errors.New(claudeControlError(response.Error))
				}
				if len(response.Response) == 0 || string(response.Response) == "null" {
					return nil, errors.New("empty_control_response")
				}
				return response.Response, nil
			}
		}
	}
	if _, err := request("1", "initialize", false); err != nil {
		return usageSnapshot{}, err
	}
	stage = "get_usage"
	raw, err := request("2", "get_usage", true)
	if err != nil {
		return usageSnapshot{}, err
	}
	return parseClaudeUsage(raw)
}

func claudeControlError(raw json.RawMessage) string {
	text := strings.ToLower(string(raw))
	for _, category := range []string{"401", "403", "429", "not supported", "not logged", "authentication"} {
		if strings.Contains(text, category) {
			return "control_error_" + strings.ReplaceAll(category, " ", "_")
		}
	}
	return "control_error"
}

type claudeProbeError struct {
	message string
	cause   error
}

func (e *claudeProbeError) Error() string { return e.message }
func (e *claudeProbeError) Unwrap() error { return e.cause }

func claudeFailure(stage string, cause, waitErr error, stderr *cliStderr) error {
	category := "io_or_process_error"
	switch {
	case errors.Is(cause, context.DeadlineExceeded):
		category = "timeout"
	case errors.Is(cause, context.Canceled):
		category = "canceled"
	case errors.Is(cause, io.EOF):
		category = "eof"
	case errors.Is(cause, exec.ErrNotFound), errors.Is(cause, os.ErrNotExist):
		category = "binary_not_found"
	case errors.Is(cause, os.ErrPermission):
		category = "permission_denied"
	default:
		// Only allow errors generated here; never include arbitrary CLI text.
		for _, known := range []string{"invalid_usage_json", "usage_unavailable", "invalid_utilization",
			"invalid_reset_timestamp", "stdin_write_failed", "invalid_control_json", "empty_control_response",
			"control_error", "control_error_401", "control_error_403", "control_error_429",
			"control_error_not_supported", "control_error_not_logged", "control_error_authentication"} {
			if cause.Error() == known {
				category = known
				break
			}
		}
	}
	exit := "unavailable"
	if waitErr == nil && stage != "start" {
		exit = "0"
	} else {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exit = fmt.Sprint(exitErr.ExitCode())
		}
	}
	return &claudeProbeError{message: fmt.Sprintf("claude %s: %s; process exit: %s; stderr: %s", stage, category, exit, stderr.summary()), cause: cause}
}
