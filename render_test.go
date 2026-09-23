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

func TestDashboardStates(t *testing.T) {
	initial := renderDashboard(nil, time.Time{}, true)
	if !strings.Contains(initial, "⚪ Account 1\n5-Hour   —\nWeekly   —\nInitializing...") {
		t.Fatal(initial)
	}
	snapshot := &usageSnapshot{
		FiveHour: usageWindow{UsedPercent: 82, ResetsAt: 1900000000},
		Weekly:   usageWindow{UsedPercent: 61, ResetsAt: 2000000000},
	}
	last := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		failed bool
		want   string
	}{
		{false, "🟢 Account 1"}, {true, "🔴 Account 1"},
	} {
		got := renderDashboard(snapshot, last, tc.failed)
		for _, part := range []string{
			dashboardTitle, tc.want,
			"5-Hour   █░░░░░░░░░  18%   Reset <t:1900000000:t> (<t:1900000000:R>)",
			"Weekly   ███░░░░░░░  39%   Reset <t:2000000000:d> (<t:2000000000:R>)",
			"<t:1800000000:R>", "⚪ Account 2", "🔴 Account 3",
			"5-Hour   ██░░░░░░░░  28%   Reset 00:03 (in 22m)",
			"Weekly   ░░░░░░░░░░  7%   Reset Sep 24 (in 1d)",
			"Update failed · Last updated 29m ago",
		} {
			if !strings.Contains(got, part) {
				t.Fatalf("missing %q in %s", part, got)
			}
		}
		if tc.failed && !strings.Contains(got, "Update failed · Last updated <t:1800000000:R>") {
			t.Fatal(got)
		}
	}
}
