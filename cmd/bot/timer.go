package main

import "time"

type timerStatus uint8

const (
	timerUnknown timerStatus = iota
	timerActive
	timerInactive
)

const timerWindow = 5 * time.Hour
const timerTolerance = time.Minute
const minimumComparison = 3 * time.Minute

type accountState struct {
	Home        string
	LastUsage   *usageSnapshot
	LastSuccess time.Time
	Timer       timerStatus
	LastAttempt time.Time
	HelloAt     time.Time
	HelloReset  int64
	Failed      bool
}

func near(a, b time.Duration) bool {
	return a >= b-timerTolerance && a <= b+timerTolerance
}

func classifyTimer(previous *usageSnapshot, previousAt time.Time, current usageSnapshot, currentAt time.Time) timerStatus {
	used := current.FiveHour.UsedPercent
	if !currentAt.Before(time.Unix(current.FiveHour.ResetsAt, 0)) {
		return timerUnknown
	}
	if previous == nil {
		if used > 0 {
			return timerActive
		}
		return timerUnknown
	}
	dt := currentAt.Sub(previousAt)
	oldReset := time.Unix(previous.FiveHour.ResetsAt, 0)
	valid := dt >= minimumComparison && dt < timerWindow && currentAt.Before(oldReset) &&
		previousAt.Before(oldReset)
	if !valid {
		return timerUnknown
	}
	dr := time.Duration(current.FiveHour.ResetsAt-previous.FiveHour.ResetsAt) * time.Second
	if near(dr, dt) {
		if used > 0 {
			return timerUnknown
		}
		if previous.FiveHour.UsedPercent == 0 && near(time.Unix(current.FiveHour.ResetsAt, 0).Sub(currentAt), timerWindow) {
			return timerInactive
		}
		return timerUnknown
	}
	if near(dr, 0) {
		return timerActive
	}
	return timerUnknown
}

func (state *accountState) recordUsage(snapshot usageSnapshot, at time.Time) {
	state.expireHello(at)
	state.Timer = classifyTimer(state.LastUsage, state.LastSuccess, snapshot, at)
	state.LastUsage = &snapshot
	state.LastSuccess = at
	state.Failed = false
}

func (state *accountState) expireHello(at time.Time) {
	if !state.HelloAt.IsZero() && (at.Sub(state.HelloAt) >= timerWindow ||
		(state.HelloReset > 0 && !at.Before(time.Unix(state.HelloReset, 0)))) {
		state.HelloAt = time.Time{}
		state.HelloReset = 0
	}
}

func (state *accountState) shouldSendHello(now time.Time) bool {
	return state.Timer == timerInactive && state.LastUsage != nil &&
		state.LastUsage.Weekly.UsedPercent < 100 &&
		(state.LastAttempt.IsZero() || now.Sub(state.LastAttempt) >= timerWindow)
}
