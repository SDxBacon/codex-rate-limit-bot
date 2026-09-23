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

const validRateResult = `{"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":61,"windowDurationMins":10080,"resetsAt":2000000000},"secondary":{"usedPercent":82,"windowDurationMins":300,"resetsAt":1900000000}}}}`

func TestParseUsage(t *testing.T) {
	got, err := parseUsage(json.RawMessage(validRateResult))
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour.UsedPercent != 82 || got.FiveHour.ResetsAt != 1900000000 ||
		got.Weekly.UsedPercent != 61 || got.Weekly.ResetsAt != 2000000000 {
		t.Fatalf("wrong windows: %+v", got)
	}

	for _, raw := range []string{
		`{}`,
		`{"rateLimits":{"primary":{"usedPercent":82,"windowDurationMins":300,"resetsAt":1900000000}}}`,
		`{"rateLimits":{"primary":{"usedPercent":101,"windowDurationMins":300,"resetsAt":1900000000},"secondary":{"usedPercent":61,"windowDurationMins":10080,"resetsAt":2000000000}}}`,
		`{"rateLimits":{"primary":{"usedPercent":82,"windowDurationMins":300,"resetsAt":0},"secondary":{"usedPercent":61,"windowDurationMins":10080,"resetsAt":2000000000}}}`,
	} {
		if _, err := parseUsage(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected invalid usage for %s", raw)
		}
	}
}

func TestProbeUsageViaCodexCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	trace := filepath.Join(dir, "trace")
	script := `#!/bin/sh
test "$1" = "app-server" && test "$2" = "--stdio" || exit 2
test "$CODEX_HOME" = "$EXPECTED_CODEX_HOME" || exit 3
read init || exit 4
printf '%s\n' "$init" >> "$TRACE_FILE"
printf '{"id":1,"result":{}}\n'
read initialized || exit 5
printf '%s\n' "$initialized" >> "$TRACE_FILE"
read request || exit 6
printf '%s\n' "$request" >> "$TRACE_FILE"
printf '{"method":"account/rateLimits/updated","params":{}}\n'
printf '{"id":2,"result":%s}\n' '` + validRateResult + `'
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "account")
	t.Setenv("EXPECTED_CODEX_HOME", home)
	t.Setenv("TRACE_FILE", trace)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := probeUsage(ctx, bin, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour.UsedPercent != 82 || got.Weekly.UsedPercent != 61 {
		t.Fatalf("wrong usage: %+v", got)
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"method":"initialize"`, `"method":"initialized"`, `"method":"account/rateLimits/read"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in CLI interaction: %s", want, b)
		}
	}
}
