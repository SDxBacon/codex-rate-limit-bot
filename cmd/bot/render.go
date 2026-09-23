package main

import (
	"fmt"
	"strings"
)

const dashboardTitle = "Codex Usage Monitor"

func usageBar(percent int) string {
	filled := percent / 10
	if filled < 0 {
		filled = 0
	}
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

func remainingPercent(usedPercent int) int {
	return 100 - usedPercent
}

func timerLabel(status timerStatus) string {
	switch status {
	case timerActive:
		return ":man_running_facing_right: Active"
	case timerInactive:
		return ":stopwatch: Inactive (rolling)"
	default:
		return ":grey_question: Unknown"
	}
}

func renderAccount(name string, state accountState) string {
	snapshot := state.LastUsage
	if snapshot == nil {
		if state.Failed {
			return "🔴 " + name + "\n5-Hour   —   5-hour reset timer: ⚪ Unknown\nWeekly   —\nUpdate failed"
		}
		return "⚪ " + name + "\n5-Hour   —   5-hour reset timer: ⚪ Unknown\nWeekly   —\nInitializing..."
	} else {
		icon, update := "🟢", "Updated"
		if state.Failed {
			icon, update = "🔴", "Update failed · Last updated"
		}
		fiveHourRemaining := remainingPercent(snapshot.FiveHour.UsedPercent)
		weeklyRemaining := remainingPercent(snapshot.Weekly.UsedPercent)
		lastLine := fmt.Sprintf("%s <t:%d:R>", update, state.LastSuccess.Unix())
		if !state.HelloAt.IsZero() {
			lastLine += fmt.Sprintf(" · Bot 發送 hello <t:%d:f>", state.HelloAt.Unix())
		}
		return fmt.Sprintf("%s %s\n5-Hour   %s  %d%%   Reset <t:%d:t> (<t:%d:R>)   5-hour reset timer: %s\nWeekly   %s  %d%%   Reset <t:%d:d> (<t:%d:R>)\n%s",
			icon, name,
			usageBar(fiveHourRemaining), fiveHourRemaining, snapshot.FiveHour.ResetsAt, snapshot.FiveHour.ResetsAt, timerLabel(state.Timer),
			usageBar(weeklyRemaining), weeklyRemaining, snapshot.Weekly.ResetsAt, snapshot.Weekly.ResetsAt,
			lastLine)
	}
}

func renderDashboard(accounts []accountConfig, states map[string]accountState) string {
	sections := make([]string, 0, len(accounts)+1)
	sections = append(sections, dashboardTitle)
	for _, account := range accounts {
		state := states[account.ID]
		sections = append(sections, renderAccount(account.Name, state))
	}
	return strings.Join(sections, "\n\n")
}
