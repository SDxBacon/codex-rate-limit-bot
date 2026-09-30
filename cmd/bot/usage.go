package main

// Percent and timestamp pointers preserve unavailable values. Claude's explicit
// null reset is recorded separately from a missing reset field.
type usageWindow struct {
	UsedPercent     *float64 `json:"used_percent"`
	ResetsAt        *int64   `json:"resets_at"`
	ResetNotStarted bool     `json:"reset_not_started,omitempty"`
}

type usageSnapshot struct {
	FiveHour usageWindow `json:"five_hour"`
	Weekly   usageWindow `json:"weekly"`
}

func newUsageWindow(used float64, reset int64) usageWindow {
	return usageWindow{UsedPercent: &used, ResetsAt: &reset}
}
