package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validClaudeUsage = `{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12.25,"resets_at":"2030-03-17T17:46:40.258+08:00"},"seven_day":{"utilization":0,"resets_at":null},"seven_day_sonnet":"ignored"},"unknown":true}`

func TestParseClaudeUsage(t *testing.T) {
	got, err := parseClaudeUsage(json.RawMessage(validClaudeUsage))
	if err != nil {
		t.Fatal(err)
	}
	reset, _ := time.Parse(time.RFC3339Nano, "2030-03-17T17:46:40.258+08:00")
	if *got.FiveHour.UsedPercent != 12.25 || *got.FiveHour.ResetsAt != reset.Unix() ||
		*got.Weekly.UsedPercent != 0 || got.Weekly.ResetsAt != nil {
		t.Fatalf("wrong mapping: %+v", got)
	}
	for _, raw := range []string{
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":null,"seven_day":{"utilization":100}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":null,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":30}}}`,
	} {
		if _, err := parseClaudeUsage(json.RawMessage(raw)); err != nil {
			t.Fatalf("valid partial sample: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{}`, `null`, `[]`, `invalid`,
		`{"rate_limits_available":false,"rate_limits":{"five_hour":{"utilization":0}}}`,
		`{"rate_limits_available":null}`, `{"rate_limits_available":"true"}`,
		`{"rate_limits_available":true,"rate_limits":null}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"resets_at":"2030-01-01T00:00:00Z"}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":null},"seven_day":null}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":-1}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":100.1}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":true}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":"private-value"}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":1e999}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":"2030-01-01T00:00:00"}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":"private-value"}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":0}}}`,
		`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0},"seven_day":{"resets_at":"bad"}}}`,
	} {
		_, err := parseClaudeUsage(json.RawMessage(raw))
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("invalid usage accepted or leaked: %s: %v", raw, err)
		}
	}
}

func fakeClaude(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func claudeResponse(id, body string) string {
	return `{"type":"control_response","response":{"subtype":"success","request_id":"` + id + `","response":` + body + `}}`
}

func TestProbeClaudeProtocolAndAccountIsolation(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("TRACE_FILE", trace)
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_PROFILE",
		"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_HOST_CREDS_FILE",
		"CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_EXTRA_BODY",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"} {
		t.Setenv(key, "private-value")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "other-account")
	t.Setenv("DISABLE_AUTOUPDATER", "0")
	bin := fakeClaude(t, `
test -z "$ANTHROPIC_API_KEY$ANTHROPIC_AUTH_TOKEN$ANTHROPIC_PROFILE$CLAUDE_CODE_OAUTH_TOKEN$CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR$CLAUDE_CODE_USE_BEDROCK$CLAUDE_CODE_USE_VERTEX$CLAUDE_SECURESTORAGE_CONFIG_DIR$CLAUDE_CODE_HOST_CREDS_FILE$CLAUDE_CODE_EFFORT_LEVEL$CLAUDE_CODE_SIMPLE$CLAUDE_CODE_EXTRA_BODY$CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC" || exit 10
test "$DISABLE_AUTOUPDATER$DISABLE_TELEMETRY$DISABLE_ERROR_REPORTING" = "111" || exit 11
printf '%s\n' "$CLAUDE_CONFIG_DIR" "$PWD" "$@" >> "$TRACE_FILE"
read init || exit 12
printf '%s\n' "$init" >> "$TRACE_FILE"
printf '%s\n' '{"type":"system","subtype":"status","response":"notification"}' '`+claudeResponse("other", `{}`)+`' '`+claudeResponse("1", `{"models":[]}`)+`'
read usage || exit 13
printf '%s\n' "$usage" >> "$TRACE_FILE"
printf '%s\n' '`+claudeResponse("2", validClaudeUsage)+`'
# Any additional user frame is a test failure.
if read extra; then printf 'unexpected input' >> "$TRACE_FILE"; exit 14; fi
`)
	for _, home := range []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")} {
		if _, err := probeClaudeUsage(context.Background(), bin, home); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(trace)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var requests []map[string]any
	var works []string
	for _, line := range lines {
		if strings.HasPrefix(line, "{") && line != `{"mcpServers":{}}` {
			var frame map[string]any
			if err := json.Unmarshal([]byte(line), &frame); err != nil {
				t.Fatal(err)
			}
			requests = append(requests, frame)
		}
		if strings.Contains(line, "claude-monitor-") {
			works = append(works, line)
			if _, err := os.Stat(line); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary work directory remains: %s", line)
			}
		}
	}
	if len(requests) != 4 || len(works) != 2 || works[0] == works[1] {
		t.Fatalf("wrong requests/work directories: %s", b)
	}
	for i, frame := range requests {
		if frame["type"] != "control_request" {
			t.Fatalf("sent user prompt: %+v", frame)
		}
		r := frame["request"].(map[string]any)
		if i%2 == 0 && (frame["request_id"] != "1" || r["subtype"] != "initialize") {
			t.Fatal(frame)
		}
		if i%2 == 1 && (frame["request_id"] != "2" || r["subtype"] != "get_usage" || r["skip_behaviors"] != true) {
			t.Fatal(frame)
		}
	}
	for _, option := range []string{"-p", "stream-json", "--verbose", "--no-session-persistence", "--safe-mode",
		"--tools\n\n", "--strict-mcp-config", `{"mcpServers":{}}`, "dontAsk", filepath.Join(dir, "one"), filepath.Join(dir, "two")} {
		if !strings.Contains(string(b), option) {
			t.Fatalf("missing option/home %q", option)
		}
	}
}

func TestProbeClaudeFailuresWithholdPrivateData(t *testing.T) {
	for _, tc := range []struct{ name, response, want string }{
		{"invalid JSON", `private-token-value`, "invalid_control_json"},
		{"control error", `{"type":"control_response","response":{"subtype":"error","request_id":"1","error":"private-token-value authentication rejected 401"}}`, "control_error_401"},
		{"empty body", `{"type":"control_response","response":{"subtype":"success","request_id":"1"}}`, "empty_control_response"},
		{"invalid usage", claudeResponse("1", `{}`) + "\n" + claudeResponse("2", `{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":"private-token-value"}}}`), "invalid_usage_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := strings.SplitN(tc.response, "\n", 2)
			script := "read init\nprintf '%s\\n' '" + responses[0] + "'\n"
			if len(responses) == 2 {
				script += "read usage\nprintf '%s\\n' '" + responses[1] + "'\n"
			}
			script += "printf 'private-token-value\\npermission denied\\n' >&2\nexit 9\n"
			bin := fakeClaude(t, script)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := probeClaudeUsage(ctx, bin, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-token-value") {
				t.Fatalf("wrong or unsafe error: %v", err)
			}
		})
	}
	bin := fakeClaude(t, "read init\nprintf 'private-token-value' >&2\nexit 7\n")
	_, err := probeClaudeUsage(context.Background(), bin, t.TempDir())
	if !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "process exit: 7") || strings.Contains(err.Error(), "private-token-value") {
		t.Fatal(err)
	}
}

func TestProbeClaudeBoundsShutdownAndReapsWrapper(t *testing.T) {
	for _, mode := range []string{"uncooperative", "wrapper"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "reaped")
			t.Setenv("REAP_MARKER", marker)
			script := ""
			if mode == "wrapper" {
				script = `if [ "$1" != native ]; then
  "$0" native <&0 &
  child=$!
  wait "$child" || exit 12
  printf 'reaped' > "$REAP_MARKER"
  exit 0
fi
`
			}
			script += "read init\nprintf '%s\\n' '" + claudeResponse("1", `{}`) + "'\nread usage\nprintf '%s\\n' '" + claudeResponse("2", validClaudeUsage) + "'\n"
			if mode == "wrapper" {
				script += "while read extra; do :; done\n"
			} else {
				script += "exec sleep 30\n"
			}
			bin := fakeClaude(t, script)
			start := time.Now()
			if _, err := probeClaudeUsage(context.Background(), bin, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("shutdown not bounded")
			}
			if mode == "wrapper" {
				b, _ := os.ReadFile(marker)
				if string(b) != "reaped" {
					t.Fatal("wrapper did not reap")
				}
			}
		})
	}
}

// Opt-in only: a real subscription read can refresh CLI-managed credentials.
func TestProbeClaudeRealCLI(t *testing.T) {
	home := os.Getenv("CLAUDE_TEST_CONFIG_DIR")
	if home == "" {
		t.Skip("set CLAUDE_TEST_CONFIG_DIR to an authenticated profile for a no-prompt read")
	}
	binary := os.Getenv("CLAUDE_TEST_BINARY")
	if binary == "" {
		binary = "claude"
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	snapshot, err := probeClaudeUsage(ctx, binary, home)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FiveHour.UsedPercent == nil && snapshot.Weekly.UsedPercent == nil {
		t.Fatal("no quota observation")
	}
	t.Log("Real CLI quota observation received without a model prompt; freshness remains unverified")
}

func TestProbeClaudeDeadlineAndOutputBound(t *testing.T) {
	bin := fakeClaude(t, "read init\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := probeClaudeUsage(ctx, bin, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("unbounded deadline: %v", err)
	}
	bin = fakeClaude(t, "read init\nhead -c 2200000 /dev/zero | tr '\\000' x\nprintf '\\n'\n")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if _, err := probeClaudeUsage(ctx2, bin, t.TempDir()); err == nil {
		t.Fatal("unbounded output accepted")
	}
}
