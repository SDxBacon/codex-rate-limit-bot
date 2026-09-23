package main

import (
	"strings"
	"testing"
	"time"
)

func TestUsageBar(t *testing.T) {
	for _, tc := range []struct {
		percent int
		want    string
	}{
		{0, "░░░░░░░░░░"}, {30, "███░░░░░░░"}, {61, "██████░░░░"},
		{82, "████████░░"}, {100, "██████████"},
	} {
		if got := usageBar(tc.percent); got != tc.want {
			t.Fatalf("%d%%: got %q, want %q", tc.percent, got, tc.want)
		}
	}
}

func TestRemainingPercent(t *testing.T) {
	for _, tc := range []struct{ used, remaining int }{
		{0, 100}, {61, 39}, {82, 18}, {100, 0},
	} {
		if got := remainingPercent(tc.used); got != tc.remaining {
			t.Fatalf("used %d%%: remaining %d%%, want %d%%", tc.used, got, tc.remaining)
		}
	}
}

func TestDashboardAccountsInConfigOrder(t *testing.T) {
	accounts := []accountConfig{{ID: "second", Name: "Personal"}, {ID: "first", Name: "Work"}}
	snapshot := &usageSnapshot{FiveHour: usageWindow{UsedPercent: 82, ResetsAt: 1900000000}, Weekly: usageWindow{UsedPercent: 61, ResetsAt: 2000000000}}
	last := time.Unix(1800000000, 0)
	states := map[string]accountState{"first": {LastUsage: snapshot, LastSuccess: last, Failed: true, Timer: timerUnknown}}
	got := renderDashboard(accounts, states, last)
	if !strings.HasPrefix(got, "## Codex 額度\n\n### Personal\n> ⚪ **等待首次讀取** · 5小時計時器狀態未知\n> **5 小時**　—\n> **每週**　　—\n-# 尚無用量資料\n\n") ||
		!strings.Contains(got, "### Work\n> 🔴 **讀取失敗** · 5小時計時器狀態未知") || strings.Index(got, "Personal") > strings.Index(got, "Work") {
		t.Fatal(got)
	}
	for _, part := range []string{
		"> **5 小時**　`█░░░░░░░░░`　**18% left＊** · 將於 <t:1900000000:s>（<t:1900000000:R>）重設",
		"> **每週**　　`███░░░░░░░`　**39% left＊** · 將於 <t:2000000000:d>（<t:2000000000:R>）重設",
		"-# ＊上次成功讀取的資料 · <t:1800000000:R> 更新",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in %s", part, got)
		}
	}
}

func TestRenderAccountEscapesMarkdownName(t *testing.T) {
	got := renderAccount("Team *one* `test`", accountState{}, time.Unix(1800000000, 0))
	if !strings.HasPrefix(got, "### Team \\*one\\* \\`test\\`\n") {
		t.Fatal(got)
	}
	failed := renderAccount("Team", accountState{Failed: true}, time.Unix(1800000000, 0))
	if !strings.HasPrefix(failed, "### Team\n> 🔴 **讀取失敗**") ||
		!strings.Contains(failed, "> **5 小時**　—\n> **每週**　　—") ||
		!strings.HasSuffix(failed, "-# 尚無成功讀取資料") {
		t.Fatal(failed)
	}
}

func TestRenderAccountTimerStatesAndHello(t *testing.T) {
	snapshot := &usageSnapshot{FiveHour: usageWindow{UsedPercent: 82, ResetsAt: 1900000000}, Weekly: usageWindow{UsedPercent: 61, ResetsAt: 2000000000}}
	state := accountState{LastUsage: snapshot, LastSuccess: time.Unix(1800000000, 0)}
	for _, tc := range []struct {
		status timerStatus
		label  string
	}{
		{timerActive, "5小時計時器運作中"},
		{timerInactive, "5小時計時器未啟動（重設時間滾動）"},
		{timerUnknown, "5小時計時器狀態未知"},
	} {
		state.Timer = tc.status
		got := renderAccount("Personal", state, state.LastSuccess)
		for _, part := range []string{
			"> 🟢 **讀取正常** · " + tc.label,
			"> **5 小時**　`█░░░░░░░░░`　**18% left** · 將於 <t:1900000000:t>（<t:1900000000:R>）重設",
			"> **每週**　　`███░░░░░░░`　**39% left** · 將於 <t:2000000000:d>（<t:2000000000:R>）重設",
			"-# <t:1800000000:R> 更新",
		} {
			if !strings.Contains(got, part) {
				t.Fatalf("timer %v: missing %q in %s", tc.status, part, got)
			}
		}
		if strings.Contains(got, "Bot 嘗試 hello") {
			t.Fatal("unexpected hello line", got)
		}
	}
	state.HelloAt = time.Unix(1800000100, 0)
	if got := renderAccount("Personal", state, state.LastSuccess); !strings.Contains(got, "-# Bot 嘗試 hello：<t:1800000100:f>") {
		t.Fatal(got)
	}
	state.Failed = true
	if got := renderAccount("Personal", state, state.LastSuccess); !strings.Contains(got, "-# Bot 嘗試 hello：<t:1800000100:f>") || strings.Contains(got, "讀取正常") {
		t.Fatal(got)
	}
}

func TestWeeklyResetSwitchesToTimeWithin24Hours(t *testing.T) {
	const resetAt int64 = 2000000000
	reset := time.Unix(resetAt, 0)
	snapshot := &usageSnapshot{FiveHour: usageWindow{UsedPercent: 82, ResetsAt: 1900000000}, Weekly: usageWindow{UsedPercent: 61, ResetsAt: resetAt}}
	for _, tc := range []struct {
		name  string
		now   time.Time
		style string
	}{
		{"more than 24 hours", reset.Add(-24*time.Hour - time.Second), "d"},
		{"exactly 24 hours", reset.Add(-24 * time.Hour), "t"},
		{"less than 24 hours", reset.Add(-time.Hour), "t"},
		{"at reset", reset, "d"},
		{"past reset", reset.Add(time.Minute), "d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, failed := range []bool{false, true} {
				state := accountState{LastUsage: snapshot, LastSuccess: tc.now.Add(-time.Minute), Failed: failed}
				got := renderAccount("Personal", state, tc.now)
				want := "將於 <t:2000000000:" + tc.style + ">（<t:2000000000:R>）重設"
				if !strings.Contains(got, "> **每週**") || !strings.Contains(got, want) {
					t.Fatalf("failed=%v: missing %q in %s", failed, want, got)
				}
			}
		})
	}
}
