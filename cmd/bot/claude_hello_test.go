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

const helloInit = `{"models":[{"value":"sonnet","resolvedModel":"claude-sonnet-5-5"},{"value":"haiku","resolvedModel":"claude-haiku-4-5-20251001"}]}`
const helloSettings = `{"applied":{"model":"claude-sonnet-5-5","effort":"low"}}`

func helloScript(settings, result, tail string) string {
	return `printf '%s\n' "$CLAUDE_CONFIG_DIR" "$PWD" "$@" >> "$TRACE_FILE"
read init
printf '%s\n' "$init" >> "$TRACE_FILE"
printf '%s\n' '` + claudeResponse("1", helloInit) + `'
read settings
printf '%s\n' "$settings" >> "$TRACE_FILE"
printf '%s\n' '` + claudeResponse("2", settings) + `'
if read user; then
  printf '%s\n' "$user" >> "$TRACE_FILE"
  printf '%s\n' '{"type":"system","subtype":"status"}' '{"type":"assistant","message":{"content":"private-token-value"}}' '` + result + `'
fi
` + tail
}

func TestSendClaudeHelloProtocolSettingsAndIsolation(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("TRACE_FILE", trace)
	t.Setenv("ANTHROPIC_API_KEY", "private-token-value")
	t.Setenv("CLAUDE_CONFIG_DIR", "wrong-profile")
	options, _ := (accountConfig{}).claudeHelloOptions()
	bin := fakeClaude(t, `test -z "$ANTHROPIC_API_KEY" || exit 11
`+helloScript(helloSettings, `{"type":"result","subtype":"success","is_error":false}`, "while read extra; do printf 'unexpected extra' >> \"$TRACE_FILE\"; done\n"))
	for _, name := range []string{"one", "two"} {
		if err := sendClaudeHello(context.Background(), bin, filepath.Join(dir, name), options); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	users, controls := 0, 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "claude-monitor-") {
			if _, err := os.Stat(line); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("work directory remains")
			}
		}
		if !strings.HasPrefix(line, "{") || line == `{"mcpServers":{}}` {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		switch frame["type"] {
		case "user":
			users++
			if frame["message"].(map[string]any)["content"] != "hello" {
				t.Fatal(frame)
			}
		case "control_request":
			controls++
			request := frame["request"].(map[string]any)["subtype"]
			if request != "initialize" && request != "get_settings" {
				t.Fatal(frame)
			}
		default:
			t.Fatal(frame)
		}
	}
	if users != 2 || controls != 4 || strings.Contains(string(b), "unexpected extra") {
		t.Fatal(string(b))
	}
	for _, want := range []string{"--model\nsonnet", "--effort\nlow", "--no-session-persistence", "dontAsk", filepath.Join(dir, "one"), filepath.Join(dir, "two")} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestSendClaudeHelloRejectsUnappliedSettingsBeforePrompt(t *testing.T) {
	options, _ := (accountConfig{}).claudeHelloOptions()
	for _, tc := range []struct{ settings, want string }{
		{`{"applied":{"model":"other-private-model","effort":"low"}}`, "model_not_applied"},
		{`{"applied":{"model":"claude-sonnet-5-5","effort":null}}`, "effort_not_applied"},
		{`{"applied":{"model":"claude-sonnet-5-5","effort":"high"}}`, "effort_not_applied"},
		{`{"applied":null}`, "invalid_settings"},
	} {
		trace := filepath.Join(t.TempDir(), "trace")
		t.Setenv("TRACE_FILE", trace)
		bin := fakeClaude(t, helloScript(tc.settings, `{"type":"result","subtype":"success","is_error":false}`, ""))
		err := sendClaudeHello(context.Background(), bin, t.TempDir(), options)
		b, _ := os.ReadFile(trace)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private") || strings.Contains(string(b), `"type":"user"`) {
			t.Fatalf("unsafe validation: %v %s", err, b)
		}
	}
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("TRACE_FILE", trace)
	bin := fakeClaude(t, helloScript(`{"applied":{"model":"claude-haiku-4-5-20251001","effort":null}}`, `{"type":"result","subtype":"success","is_error":false}`, ""))
	if err := sendClaudeHello(context.Background(), bin, t.TempDir(), helloOptions{Model: "haiku"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(trace)
	if strings.Contains(string(b), "--effort") || !strings.Contains(string(b), `"type":"user"`) {
		t.Fatal(string(b))
	}
}

func TestSendClaudeHelloTerminalFailuresAndDeadline(t *testing.T) {
	options, _ := (accountConfig{}).claudeHelloOptions()
	for _, tc := range []struct{ result, tail, want string }{
		{`{"type":"result","subtype":"success","is_error":true,"errors":["private-token-value"]}`, "", "hello_failed"},
		{`{"type":"result","subtype":"error_max_turns","is_error":false}`, "", "hello_failed"},
		{`{"type":"result","subtype":"success"}`, "", "invalid_result"},
		{`{"type":"result","subtype":"success","is_error":"private-token-value"}`, "", "invalid_result"},
		{`private-token-value`, "", "invalid_control_json"},
		{`{"type":"assistant"}`, "exit 7\n", "eof"},
		{`{"type":"result","subtype":"success","is_error":false}`, "exit 7\n", "process_exit"},
	} {
		t.Setenv("TRACE_FILE", filepath.Join(t.TempDir(), "trace"))
		bin := fakeClaude(t, helloScript(helloSettings, tc.result, tc.tail))
		err := sendClaudeHello(context.Background(), bin, t.TempDir(), options)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-token-value") {
			t.Fatalf("%s: %v", tc.want, err)
		}
		if tc.want == "eof" && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	}
	t.Setenv("TRACE_FILE", filepath.Join(t.TempDir(), "trace"))
	bin := fakeClaude(t, helloScript(helloSettings, `{"type":"assistant"}`, "exec sleep 30\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := sendClaudeHello(ctx, bin, t.TempDir(), options)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatal(err)
	}
}

func TestProductionClaudeHelloBindsAccountOptions(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("TRACE_FILE", trace)
	bin := fakeClaude(t, helloScript(`{"applied":{"model":"claude-haiku-4-5-20251001","effort":null}}`, `{"type":"result","subtype":"success","is_error":false}`, ""))
	provider := makeProviders("unused", bin)[accountClaude]
	account := accountConfig{Type: accountClaude, HelloModel: "haiku", HelloEffort: json.RawMessage(`null`)}
	if err := provider.ConfigureHello(account)(context.Background(), bin, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(trace)
	if !strings.Contains(string(data), "--model\nhaiku") || strings.Contains(string(data), "--effort") {
		t.Fatal("ignored per-account options")
	}
}

func TestDefaultClaudePollingSendsHelloAndRechecks(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("TRACE_FILE", trace)
	base := time.Unix(1900000000, 0)
	reset := base.Add(timerWindow + 5*time.Minute)
	usage := `{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":"` + reset.UTC().Format(time.RFC3339) + `"},"seven_day":{"utilization":10,"resets_at":null}}}`
	bin := fakeClaude(t, `read init
printf '%s\n' "$init" >> "$TRACE_FILE"
printf '%s\n' '`+claudeResponse("1", helloInit)+`'
read request
printf '%s\n' "$request" >> "$TRACE_FILE"
case "$request" in
  *get_settings*)
    printf '%s\n' '`+claudeResponse("2", helloSettings)+`'
    read user
    printf '%s\n' "$user" >> "$TRACE_FILE"
    printf '%s\n' '{"type":"result","subtype":"success","is_error":false}'
    ;;
  *get_usage*)
    printf '%s\n' '`+claudeResponse("2", usage)+`'
    ;;
  *) exit 9;;
esac
while read extra; do printf 'unexpected extra\n' >> "$TRACE_FILE"; done
`)
	old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
	states := map[string]accountState{"one": {Type: accountClaude, Home: filepath.Join(dir, "one"), LastUsage: &old, LastSuccess: base}}
	providers := makeProviders("unused-codex", bin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probeAccounts(ctx, []accountConfig{{ID: "one", Home: "one", Type: accountClaude}}, dir, states, providers, func() time.Time { return base.Add(5 * time.Minute) }, nil)
	data, _ := os.ReadFile(trace)
	frames := string(data)
	if strings.Count(frames, `"subtype":"get_usage"`) != 2 || strings.Count(frames, `"subtype":"get_settings"`) != 1 ||
		strings.Count(frames, `"type":"user"`) != 1 || strings.Contains(frames, "unexpected") || states["one"].LastAttempt.IsZero() || states["one"].Failed {
		t.Fatalf("default lifecycle did not run: %s state=%+v", data, states["one"])
	}
}
