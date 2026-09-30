package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMixedAccountsDispatchFailuresAndRecovery(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1900000000, 0)
	accounts := []accountConfig{
		{ID: "claude-one", Home: "claude-one", Type: accountClaude},
		{ID: "codex", Home: "codex"},
		{ID: "claude-two", Home: "claude-two", Type: accountClaude},
	}
	states := map[string]accountState{}
	var calls []string
	fail := false
	providers := map[string]accountProvider{
		accountCodex: {Binary: "codex-bin", Probe: func(ctx context.Context, binary, home string) (usageSnapshot, error) {
			calls = append(calls, binary+":"+filepath.Base(home))
			return testSnapshot(10, 100, base.Add(timerWindow).Unix()), nil
		}},
		accountClaude: {Binary: "claude-bin", Probe: func(ctx context.Context, binary, home string) (usageSnapshot, error) {
			calls = append(calls, binary+":"+filepath.Base(home))
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > probeTimeout {
				t.Fatal("no bounded query")
			}
			if fail && filepath.Base(home) == "claude-one" {
				return usageSnapshot{}, errors.New("usage_unavailable")
			}
			return usageSnapshot{Weekly: newUsageWindow(12.25, base.Add(timerWindow).Unix())}, nil
		}},
	}
	probeAccounts(context.Background(), accounts, root, states, providers, func() time.Time { return base }, nil)
	if len(calls) != 3 || calls[0] != "claude-bin:claude-one" || calls[1] != "codex-bin:codex" || calls[2] != "claude-bin:claude-two" {
		t.Fatal(calls)
	}
	old := states["claude-one"].LastUsage
	fail = true
	probeAccounts(context.Background(), accounts, root, states, providers, func() time.Time { return base.Add(5 * time.Minute) }, nil)
	if !states["claude-one"].Failed || states["claude-one"].LastUsage != old || !states["claude-one"].LastSuccess.Equal(base) {
		t.Fatal("failed account lost old observation")
	}
	if states["codex"].Failed || !states["claude-two"].LastSuccess.Equal(base.Add(5*time.Minute)) {
		t.Fatal("failure blocked following accounts")
	}
	fail = false
	probeAccounts(context.Background(), accounts[:1], root, states, providers, func() time.Time { return base.Add(10 * time.Minute) }, nil)
	if states["claude-one"].Failed || len(states) != 1 || !states["claude-one"].LastSuccess.Equal(base.Add(10*time.Minute)) {
		t.Fatal(states)
	}
}

func TestClaudeNeverClassifiesTimerOrSendsHello(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1900000000, 0)
	for _, name := range []string{"fixed zero", "rolling zero", "null reset", "expired reset", "used"} {
		t.Run(name, func(t *testing.T) {
			old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
			states := map[string]accountState{"one": {Type: accountClaude, Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base}}
			sent := 0
			providers := map[string]accountProvider{accountClaude: {Binary: "claude", Hello: func(context.Context, string, string) error { sent++; return nil }, Probe: func(context.Context, string, string) (usageSnapshot, error) {
				s := testSnapshot(0, 10, base.Add(timerWindow).Unix())
				switch name {
				case "rolling zero":
					s.FiveHour = newUsageWindow(0, base.Add(timerWindow+5*time.Minute).Unix())
				case "null reset":
					s.FiveHour.ResetsAt = nil
				case "expired reset":
					s.FiveHour = newUsageWindow(0, base.Add(-time.Hour).Unix())
				case "used":
					s.FiveHour = newUsageWindow(10, base.Add(timerWindow).Unix())
				}
				return s, nil
			}}}
			accounts := []accountConfig{{ID: "one", Home: "one", Type: accountClaude}}
			probeAccounts(context.Background(), accounts, root, states, providers, func() time.Time { return base.Add(5 * time.Minute) }, nil)
			if sent != 0 || states["one"].Timer != timerUnknown || !states["one"].HelloAt.IsZero() {
				t.Fatal(states["one"])
			}
			state := states["one"]
			state.Timer = timerInactive
			if state.shouldSendHello(base.Add(6 * time.Minute)) {
				t.Fatal("Claude passed hello gate")
			}
		})
	}
}

func TestProviderAndHomeChangesResetAllEvidence(t *testing.T) {
	root := t.TempDir()
	base := time.Unix(1900000000, 0)
	for _, tc := range []struct{ name, oldType, newType, newHome string }{
		{"codex to claude", accountCodex, accountClaude, "one"},
		{"claude to codex", accountClaude, accountCodex, "one"},
		{"claude home change", accountClaude, accountClaude, "two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := testSnapshot(0, 10, base.Add(timerWindow).Unix())
			states := map[string]accountState{"one": {Type: tc.oldType, Home: filepath.Join(root, "one"), LastUsage: &old, LastSuccess: base, Timer: timerInactive, HelloAt: base, HelloReset: *old.FiveHour.ResetsAt, LastAttempt: base}}
			providers := map[string]accountProvider{tc.newType: {Probe: func(context.Context, string, string) (usageSnapshot, error) {
				return usageSnapshot{}, errors.New("unavailable")
			}}}
			probeAccounts(context.Background(), []accountConfig{{ID: "one", Home: tc.newHome, Type: accountType(tc.newType)}}, root, states, providers, func() time.Time { return base.Add(5 * time.Minute) }, nil)
			state := states["one"]
			if state.Type != tc.newType || state.Home != filepath.Join(root, tc.newHome) || state.LastUsage != nil || !state.LastSuccess.IsZero() || !state.LastAttempt.IsZero() || !state.HelloAt.IsZero() || state.HelloReset != 0 || state.Timer != timerUnknown || !state.Failed {
				t.Fatalf("old evidence leaked: %+v", state)
			}
		})
	}
}
