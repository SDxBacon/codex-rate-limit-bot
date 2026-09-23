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
	states := map[string]accountState{"first": {LastUsage: snapshot, LastSuccess: last}}
	got := renderDashboard(accounts, states, map[string]bool{"first": true})
	if !strings.Contains(got, "⚪ Personal\n5-Hour   —\nWeekly   —\nInitializing...") ||
		!strings.Contains(got, "🔴 Work") || strings.Index(got, "Personal") > strings.Index(got, "Work") {
		t.Fatal(got)
	}
	for _, part := range []string{
		"5-Hour   █░░░░░░░░░  18%   Reset <t:1900000000:t> (<t:1900000000:R>)",
		"Weekly   ███░░░░░░░  39%   Reset <t:2000000000:d> (<t:2000000000:R>)",
		"Update failed · Last updated <t:1800000000:R>",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in %s", part, got)
		}
	}
	if strings.Contains(got, "Account 3") {
		t.Fatal("fixed template remains", got)
	}
}
