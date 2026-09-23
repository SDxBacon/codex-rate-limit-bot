package main

import (
	"fmt"
	"strings"
	"time"
)

const dashboardTitle = "Codex Usage Monitor" // Legacy heading, kept for message recovery.
const legacyDashboardHeading = "**" + dashboardTitle + "**"
const dashboardHeading = "## Codex 額度"

var discordMarkdownEscaper = strings.NewReplacer(
	"\\", "\\\\", "*", "\\*", "_", "\\_", "~", "\\~", "`", "\\`", "|", "\\|",
)

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
		return "5小時計時器運作中"
	case timerInactive:
		return "5小時計時器未啟動（重設時間滾動）"
	default:
		return "5小時計時器狀態未知"
	}
}

func resetAtLabel(resetsAt int64, absoluteStyle string) string {
	return fmt.Sprintf("將於 <t:%d:%s>（<t:%d:R>）重設", resetsAt, absoluteStyle, resetsAt)
}

func weeklyResetStyle(resetsAt int64, now time.Time) string {
	remaining := time.Unix(resetsAt, 0).Sub(now)
	if remaining > 0 && remaining <= 24*time.Hour {
		return "t"
	}
	return "d"
}

func helloLine(state accountState) string {
	if state.HelloAt.IsZero() {
		return ""
	}
	return fmt.Sprintf("\n-# Bot 嘗試 hello：<t:%d:f>", state.HelloAt.Unix())
}

func renderAccount(name string, state accountState, now time.Time) string {
	snapshot := state.LastUsage
	heading := "### " + discordMarkdownEscaper.Replace(name)
	if snapshot == nil {
		if state.Failed {
			return heading + "\n> 🔴 **讀取失敗** · 5小時計時器狀態未知\n> **5 小時**　—\n> **每週**　　—\n-# 尚無成功讀取資料"
		}
		return heading + "\n> ⚪ **等待首次讀取** · 5小時計時器狀態未知\n> **5 小時**　—\n> **每週**　　—\n-# 尚無用量資料"
	}
	fiveHourRemaining := remainingPercent(snapshot.FiveHour.UsedPercent)
	weeklyRemaining := remainingPercent(snapshot.Weekly.UsedPercent)
	if state.Failed {
		return fmt.Sprintf("%s\n> 🔴 **讀取失敗** · 5小時計時器狀態未知\n> **5 小時**　`%s`　**%d%% left＊** · %s\n> **每週**　　`%s`　**%d%% left＊** · %s\n-# ＊上次成功讀取的資料 · <t:%d:R> 更新%s",
			heading, usageBar(fiveHourRemaining), fiveHourRemaining, resetAtLabel(snapshot.FiveHour.ResetsAt, "s"),
			usageBar(weeklyRemaining), weeklyRemaining, resetAtLabel(snapshot.Weekly.ResetsAt, weeklyResetStyle(snapshot.Weekly.ResetsAt, now)), state.LastSuccess.Unix(), helloLine(state))
	}
	return fmt.Sprintf("%s\n> 🟢 **讀取正常** · %s\n> **5 小時**　`%s`　**%d%% left** · %s\n> **每週**　　`%s`　**%d%% left** · %s\n-# <t:%d:R> 更新%s",
		heading, timerLabel(state.Timer), usageBar(fiveHourRemaining), fiveHourRemaining,
		resetAtLabel(snapshot.FiveHour.ResetsAt, "t"), usageBar(weeklyRemaining), weeklyRemaining,
		resetAtLabel(snapshot.Weekly.ResetsAt, weeklyResetStyle(snapshot.Weekly.ResetsAt, now)), state.LastSuccess.Unix(), helloLine(state))
}

func renderDashboard(accounts []accountConfig, states map[string]accountState, now time.Time) string {
	sections := make([]string, 0, len(accounts)+1)
	sections = append(sections, dashboardHeading)
	for _, account := range accounts {
		state := states[account.ID]
		sections = append(sections, renderAccount(account.Name, state, now))
	}
	return strings.Join(sections, "\n\n")
}
