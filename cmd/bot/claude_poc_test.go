package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validatedClaudeExperiment() claudePOCReport {
	base := time.Unix(1900000000, 0)
	fresh := claudePOCDiagnostics{EndpointAttempts: 1, EndpointSuccesses: 1}
	effort := "low"
	helloAt := base.Add(10*time.Minute + time.Second)
	return claudePOCReport{Schema: 1, CLIVersion: "2.1.284", OS: "linux", Arch: "arm64", UID: 1000, Home: "/accounts/one",
		Hello: &claudePOCHello{At: helloAt, CompletedAt: helloAt.Add(time.Second), Model: "sonnet", Effort: &effort},
		Samples: []claudePOCObservation{
			{At: base, Usage: testSnapshot(1, 10, base.Add(time.Minute).Unix()), Diagnostics: fresh},
			{At: base.Add(5 * time.Minute), Usage: testClaudeIdleSnapshot(10), Diagnostics: fresh},
			{At: base.Add(10 * time.Minute), Usage: testClaudeIdleSnapshot(10), Diagnostics: fresh},
			{At: helloAt.Add(2 * time.Second), Usage: testSnapshot(0, 10, helloAt.Add(timerWindow).Unix()), Diagnostics: fresh},
			{At: helloAt.Add(5*time.Minute + 2*time.Second), Usage: testSnapshot(0, 10, helloAt.Add(timerWindow).Unix()), Diagnostics: fresh},
		},
	}
}

func TestClaudeExperimentRequiresRealTransitionEvidence(t *testing.T) {
	if got := analyzeClaudePOC(validatedClaudeExperiment()); got.Status != "validated" {
		t.Fatal(got)
	}
	for _, name := range []string{"wrong platform", "root", "no expiry", "no active window", "cached before", "cached after", "endpoint failed", "missing reset", "non-null idle reset", "no hello", "failed hello", "unsupported effort", "still rolling", "short followup", "old post reset"} {
		t.Run(name, func(t *testing.T) {
			r := validatedClaudeExperiment()
			switch name {
			case "wrong platform":
				r.OS = "darwin"
			case "root":
				r.UID = 0
			case "no expiry":
				r.Samples = r.Samples[1:]
			case "no active window":
				r.Samples[0].Usage.FiveHour.UsedPercent = nil
			case "cached before":
				r.Samples[1].Diagnostics.CacheHit = true
			case "cached after":
				r.Samples[4].Diagnostics.EndpointSuccesses = 0
			case "endpoint failed":
				r.Samples[4].Diagnostics.FetchFailed = true
			case "missing reset":
				r.Samples[2].Usage.FiveHour.ResetNotStarted = false
			case "non-null idle reset":
				r.Samples[2].Usage.FiveHour = newUsageWindow(0, r.Samples[2].At.Add(timerWindow).Unix())
			case "no hello":
				r.Hello = nil
			case "failed hello":
				r.Hello.Failure = "hello_failed"
			case "unsupported effort":
				r.Hello.Effort = nil
			case "still rolling":
				r.Samples[4].Usage.FiveHour.ResetsAt = newUsageWindow(0, r.Samples[4].At.Add(timerWindow).Unix()).ResetsAt
			case "short followup":
				r.Samples[4].At = r.Samples[3].At.Add(time.Minute)
			case "old post reset":
				r.Samples[3].Usage.FiveHour.ResetsAt = newUsageWindow(0, r.Hello.At.Add(4*time.Hour).Unix()).ResetsAt
				r.Samples[4].Usage.FiveHour.ResetsAt = r.Samples[3].Usage.FiveHour.ResetsAt
			}
			if got := analyzeClaudePOC(r); got.Status != "unknown" {
				t.Fatalf("false validation: %+v", got)
			}
		})
	}
}

func TestClaudePOCManualHelloExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("TRACE_FILE", trace)
	bin := fakeClaude(t, `if [ "$1" = --version ]; then printf '2.1.284 (Claude Code)\n'; exit 0; fi
debug=''
model=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --debug-file) shift; debug="$1";;
    --model) shift; model="$1";;
  esac
  shift
done
if [ -n "$debug" ]; then printf 'fetchUtilization: GET /api/oauth/usage\nfetchUtilization: 200\n' > "$debug"; fi
read init
printf '%s\n' '`+claudeResponse("1", helloInit)+`'
read second
if [ -n "$model" ]; then
  printf '%s\n' '`+claudeResponse("2", helloSettings)+`'
  read user
  printf '%s\n' "$user" >> "$TRACE_FILE"
  printf '%s\n' '{"type":"result","subtype":"success","is_error":false}'
else
  printf '%s\n' '`+claudeResponse("2", validClaudeUsage)+`'
fi
while read extra; do printf 'unexpected extra\n' >> "$TRACE_FILE"; done
`)
	output := filepath.Join(dir, "report.json")
	args := []string{"--binary", bin, "--config-dir", dir, "--output", output, "--hello"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runClaudePOC(ctx, args); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(output)
	var report claudePOCReport
	if json.Unmarshal(data, &report) != nil || report.Hello == nil || report.Hello.Failure != "" || len(report.Samples) != 2 || report.Verdict.Status == "validated" {
		t.Fatalf("hello alone was misclassified: %s", data)
	}
	if err := runClaudePOC(ctx, append(args, "--compare", output)); err == nil || !strings.Contains(err.Error(), "already_attempted") {
		t.Fatalf("duplicate allowed: %v", err)
	}
	frames, _ := os.ReadFile(trace)
	if strings.Count(string(frames), `"type":"user"`) != 1 || strings.Contains(string(frames), "unexpected") {
		t.Fatal("repeated prompt")
	}
	// Continue observations after the manual attempt without sending another one.
	if err := runClaudePOC(ctx, []string{"--binary", bin, "--config-dir", dir, "--output", output, "--compare", output}); err != nil {
		t.Fatal(err)
	}
	frames, _ = os.ReadFile(trace)
	if strings.Count(string(frames), `"type":"user"`) != 1 {
		t.Fatal("continuation resent hello")
	}
}

func TestClaudePOCCommandControlOnlyAndPrivateReport(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "profile")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "trace")
	t.Setenv("TRACE_FILE", trace)
	bin := fakeClaude(t, `if [ "$1" = --version ]; then printf '2.1.284 (Claude Code)\n'; exit 0; fi
debug=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = --debug-file ]; then shift; debug="$1"; fi
  shift
done
printf 'private-token-value\nfetchUtilization: GET /api/oauth/usage\nfetchUtilization: 200\n' > "$debug"
read init
printf '%s\n' "$init" >> "$TRACE_FILE"
printf '%s\n' '`+claudeResponse("1", helloInit)+`'
read usage
printf '%s\n' "$usage" >> "$TRACE_FILE"
printf '%s\n' '`+claudeResponse("2", validClaudeUsage)+`'
while read extra; do printf '%s\n' "$extra" >> "$TRACE_FILE"; done
`)
	output := filepath.Join(dir, "report.json")
	args := []string{"--binary", bin, "--config-dir", home, "--output", output}
	if err := runClaudePOC(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe report permissions: %v %v", info, err)
	}
	data, _ := os.ReadFile(output)
	var report claudePOCReport
	if json.Unmarshal(data, &report) != nil || len(report.Samples) != 1 || !report.Samples[0].endpointConfirmed() || report.Verdict.Status != "unknown" || strings.Contains(string(data), "private-token-value") {
		t.Fatalf("wrong or unsafe report: %s", data)
	}
	if err := runClaudePOC(context.Background(), args); err == nil {
		t.Fatal("silently overwrote experiment")
	}
	if err := runClaudePOC(context.Background(), append(args, "--compare", output)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(output)
	if json.Unmarshal(data, &report) != nil || len(report.Samples) != 2 {
		t.Fatal("continuation lost history")
	}
	frames, _ := os.ReadFile(trace)
	if strings.Contains(string(frames), `"type":"user"`) {
		t.Fatal("sampling sent hello")
	}
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := runClaudePOC(context.Background(), []string{"--binary", bin, "--config-dir", other, "--output", filepath.Join(dir, "other.json"), "--compare", output}); err == nil {
		t.Fatal("accepted another profile's evidence")
	}
}
