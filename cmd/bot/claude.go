package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"
)

type claudeRateWindow struct {
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
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
		if bytes.Equal(bytes.TrimSpace(w.ResetsAt), []byte("null")) {
			window.ResetNotStarted = true
		} else if len(w.ResetsAt) != 0 {
			var timestamp string
			if err := json.Unmarshal(w.ResetsAt, &timestamp); err != nil {
				return usageWindow{}, errors.New("invalid_usage_json")
			}
			at, err := time.Parse(time.RFC3339Nano, timestamp)
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

// Claude reports an unstarted window with an explicit null reset. A future
// reset means its window is active, even when utilization rounds to zero.
func classifyClaudeTimer(current usageSnapshot, at time.Time) timerStatus {
	window := current.FiveHour
	if window.UsedPercent == nil {
		return timerUnknown
	}
	if window.ResetsAt != nil {
		if at.Before(time.Unix(*window.ResetsAt, 0)) {
			return timerActive
		}
		return timerUnknown
	}
	if window.ResetNotStarted && *window.UsedPercent == 0 {
		return timerInactive
	}
	return timerUnknown
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

// A CLI observation may be cached; it contains no freshness metadata.
func probeClaudeUsage(ctx context.Context, binary, home string) (usageSnapshot, error) {
	return probeClaudeUsageWithOptions(ctx, binary, home, claudeSessionOptions{})
}

func probeClaudeUsageWithOptions(ctx context.Context, binary, home string, options claudeSessionOptions) (snapshot usageSnapshot, err error) {
	err = withClaudeSession(ctx, binary, home, options, func(session *claudeSession) error {
		if _, err := session.request("initialize", false); err != nil {
			return err
		}
		raw, err := session.request("get_usage", true)
		if err != nil {
			return err
		}
		snapshot, err = parseClaudeUsage(raw)
		return err
	})
	return snapshot, err
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
			"control_error_not_supported", "control_error_not_logged", "control_error_authentication",
			"invalid_settings", "model_not_applied", "effort_not_applied", "invalid_result", "hello_failed", "process_exit"} {
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
