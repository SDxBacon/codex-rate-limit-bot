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

func TestProbeUsageLetsWrapperReapChild(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	marker := filepath.Join(dir, "reaped")
	script := `#!/bin/sh
if [ "$1" != native ]; then
  "$0" native <&0 &
  child=$!
  wait "$child" || exit 12
  printf 'reaped' > "$REAP_MARKER"
  exit 0
fi
read init || exit 1
printf '{"id":1,"result":{}}\n'
read initialized || exit 2
read request || exit 3
printf '{"id":2,"result":%s}\n' '` + validRateResult + `'
while IFS= read -r line; do :; done
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REAP_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		_ = os.Remove(marker)
		if _, err := probeUsage(ctx, bin, dir); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(marker); err != nil || string(b) != "reaped" {
			t.Fatalf("wrapper did not reap its child: %q, %v", b, err)
		}
	}
}

func TestProbeUsageReportsExitWithoutSecrets(t *testing.T) {
	for _, stage := range []string{"initialize", "account/rateLimits/read"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "codex")
			script := "#!/bin/sh\nread init\n"
			if stage != "initialize" {
				script += "printf '{\"id\":1,\"result\":{}}\\n'\nread initialized\nread request\n"
			}
			script += "printf 'private-token-value\\nError: Resource temporarily unavailable (os error 11)\\n' >&2\nexit 11\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := probeUsage(ctx, bin, dir)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("expected EOF, got %v", err)
			}
			for _, want := range []string{stage, "exit status 11", "resource temporarily unavailable"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q in %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "private-token-value") {
				t.Fatalf("stderr secret leaked: %v", err)
			}
		})
	}
}

func TestProbeUsageBoundsUncooperativeShutdown(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	script := `#!/bin/sh
read init
printf '{"id":1,"result":{}}\n'
read initialized
read request
printf '{"id":2,"result":%s}\n' '` + validRateResult + `'
exec sleep 30
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := probeUsage(ctx, bin, dir); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("shutdown took %v", elapsed)
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
