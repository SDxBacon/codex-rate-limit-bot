package main

// nil means unavailable, never zero utilization or an epoch-zero reset.
type usageWindow struct {
	UsedPercent *float64 `json:"used_percent"`
	ResetsAt    *int64   `json:"resets_at"`
}

type usageSnapshot struct {
	FiveHour usageWindow `json:"five_hour"`
	Weekly   usageWindow `json:"weekly"`
}

func newUsageWindow(used float64, reset int64) usageWindow {
	return usageWindow{UsedPercent: &used, ResetsAt: &reset}
}
