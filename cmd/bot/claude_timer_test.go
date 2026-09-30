package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testClaudeIdleSnapshot(weekly float64) usageSnapshot {
	window := newUsageWindow(0, 1)
	window.ResetsAt = nil
	window.ResetNotStarted = true
	return usageSnapshot{FiveHour: window, Weekly: newUsageWindow(weekly, 2000000000)}
}

func TestClaudeTimerUsesOnlyDirectWindows(t *testing.T) {
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, window string
		want         timerStatus
		nullReset    bool
	}{
		{"unstarted", `{"utilization":0,"resets_at":null}`, timerInactive, true},
		{"missing reset", `{"utilization":0}`, timerUnknown, false},
		{"missing percent", `{"resets_at":null}`, timerUnknown, true},
		{"unavailable window", `null`, timerUnknown, false},
		{"contradictory used null", `{"utilization":1,"resets_at":null}`, timerUnknown, true},
		{"active zero", `{"utilization":0,"resets_at":"2030-01-01T05:00:00Z"}`, timerActive, false},
		{"active used", `{"utilization":20,"resets_at":"2030-01-01T05:00:00Z"}`, timerActive, false},
		{"expired reset", `{"utilization":0,"resets_at":"2029-12-31T23:59:59Z"}`, timerUnknown, false},
		{"at reset", `{"utilization":0,"resets_at":"2030-01-01T00:00:00Z"}`, timerUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately conflicting extra fields must not affect the result.
			raw := `{"rate_limits_available":true,"rate_limits":{"five_hour":` + tc.window +
				`,"seven_day":{"utilization":1,"resets_at":null},"limits":[{"kind":"session","is_active":true}],"seven_day_opus":"ignored"}}`
			snapshot, err := parseClaudeUsage(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.FiveHour.ResetNotStarted != tc.nullReset || !snapshot.Weekly.ResetNotStarted {
				t.Fatalf("null/missing reset distinction lost: %+v", snapshot)
			}
			state := accountState{Type: accountClaude}
			state.recordUsage(snapshot, at)
			if state.Timer != tc.want || state.Failed {
				t.Fatalf("timer=%v want=%v failed=%v", state.Timer, tc.want, state.Failed)
			}
			// No historical samples or minimum comparison interval are needed.
			state.recordUsage(snapshot, at.Add(time.Second))
			if state.Timer != tc.want {
				t.Fatalf("second observation changed timer: %v", state.Timer)
			}
		})
	}
}

func TestClaudeHelloGateUsesNullResetAndWeeklyQuota(t *testing.T) {
	at := time.Unix(1900000000, 0)
	for _, name := range []string{"idle", "weekly missing", "weekly full", "missing reset", "active zero", "used null", "failed", "cooldown", "cooldown elapsed"} {
		t.Run(name, func(t *testing.T) {
			snapshot := testClaudeIdleSnapshot(1)
			state := accountState{Type: accountClaude}
			want := false
			switch name {
			case "idle", "cooldown elapsed":
				want = true
			case "weekly missing":
				snapshot.Weekly.UsedPercent = nil
			case "weekly full":
				snapshot.Weekly = newUsageWindow(100, 2000000000)
			case "missing reset":
				snapshot.FiveHour.ResetNotStarted = false
			case "active zero":
				snapshot.FiveHour = newUsageWindow(0, at.Add(timerWindow).Unix())
			case "used null":
				snapshot.FiveHour.UsedPercent = newUsageWindow(1, 1).UsedPercent
			}
			state.recordUsage(snapshot, at)
			switch name {
			case "failed":
				state.Failed = true
			case "cooldown":
				state.LastAttempt = at.Add(-time.Minute)
			case "cooldown elapsed":
				state.LastAttempt = at.Add(-timerWindow)
			}
			if got := state.shouldSendHello(at); got != want {
				t.Fatalf("hello=%v want=%v", got, want)
			}
		})
	}
}

func TestClaudeNullResetHelloRecheckAndCooldown(t *testing.T) {
	base := time.Unix(1900000000, 0)
	for _, outcome := range []string{"active zero", "active used", "still unstarted", "missing reset", "recheck failure", "hello failure"} {
		t.Run(outcome, func(t *testing.T) {
			now := base
			reads, sent, publishes := 0, 0, 0
			states := make(map[string]accountState)
			idle := testClaudeIdleSnapshot(1)
			providers := map[string]accountProvider{accountClaude: {
				Probe: func(context.Context, string, string) (usageSnapshot, error) {
					reads++
					if reads != 2 {
						return idle, nil
					}
					if publishes != 0 {
						t.Fatal("publication delayed recheck")
					}
					snapshot := idle
					switch outcome {
					case "active zero":
						snapshot.FiveHour = newUsageWindow(0, now.Add(timerWindow).Unix())
					case "active used":
						snapshot.FiveHour = newUsageWindow(1, now.Add(timerWindow).Unix())
					case "missing reset":
						snapshot.FiveHour.ResetNotStarted = false
					case "recheck failure":
						return usageSnapshot{}, errors.New("unavailable")
					}
					return snapshot, nil
				},
				Hello: func(context.Context, string, string) error {
					sent++
					if outcome == "hello failure" {
						return errors.New("hello_failed")
					}
					return nil
				},
			}}
			accounts := []accountConfig{{ID: "claude", Home: "claude", Type: accountClaude}}
			// Keep the account path stable across cycles to retain the cooldown.
			root := t.TempDir()
			poll := func() {
				probeAccounts(context.Background(), accounts, root, states, providers,
					func() time.Time { return now }, func() { publishes++ })
			}
			poll()
			want := timerInactive
			if strings.HasPrefix(outcome, "active") {
				want = timerActive
			} else if outcome == "missing reset" || outcome == "recheck failure" {
				want = timerUnknown
			}
			state := states["claude"]
			if reads != 2 || sent != 1 || publishes != 1 || state.Timer != want ||
				state.Failed != (outcome == "recheck failure") {
				t.Fatalf("first null-reset lifecycle failed: reads=%d sent=%d state=%+v", reads, sent, state)
			}
			if want == timerActive && state.HelloReset != now.Add(timerWindow).Unix() {
				t.Fatal("new reset was not recorded after hello")
			}
			now = base.Add(5 * time.Minute)
			poll()
			if sent != 1 || !states["claude"].LastAttempt.Equal(base) {
				t.Fatal("null reset bypassed cooldown")
			}
			now = base.Add(timerWindow)
			poll()
			if sent != 2 {
				t.Fatal("hello did not resume after cooldown")
			}
		})
	}
}

func TestClaudeNullResetRendering(t *testing.T) {
	at := time.Unix(1900000000, 0)
	state := accountState{Type: accountClaude}
	state.recordUsage(testClaudeIdleSnapshot(1), at)
	got := renderAccount("Claude", state, at)
	for _, want := range []string{"5小時計時器未啟動", "100% left** · 計時器未啟動", "額度可能為快取"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "重設時間滾動") || strings.Contains(got, "5小時計時器狀態未知") {
		t.Fatal(got)
	}
	// Both direct windows preserve explicit null independently of missing fields.
	snapshot, err := parseClaudeUsage(json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":0,"resets_at":null},"seven_day":{"utilization":0,"resets_at":null}}}`))
	if err != nil {
		t.Fatal(err)
	}
	state.recordUsage(snapshot, at)
	if got := renderAccount("Claude", state, at); strings.Count(got, "100% left** · 計時器未啟動") != 2 {
		t.Fatal(got)
	}
	state.Failed = true
	if got := renderAccount("Claude", state, at); !strings.Contains(got, "5小時計時器狀態未知") || !strings.Contains(got, "100% left＊") {
		t.Fatal(got)
	}
}
