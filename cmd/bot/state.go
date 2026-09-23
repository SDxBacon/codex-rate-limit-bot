package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type savedState struct {
	MessageID string                  `json:"message_id"`
	Accounts  map[string]accountState `json:"accounts,omitempty"`
}

type accountState struct {
	LastUsage   *usageSnapshot `json:"last_usage,omitempty"`
	LastSuccess time.Time      `json:"last_success,omitempty"`
}

func loadState(path string) (savedState, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return savedState{}, nil
	}
	if err != nil {
		return savedState{}, err
	}
	var disk struct {
		savedState
		LastUsage   *usageSnapshot `json:"last_usage"`
		LastSuccess time.Time      `json:"last_success"`
	}
	if err := json.Unmarshal(b, &disk); err != nil {
		return savedState{}, fmt.Errorf("decode state: %w", err)
	}
	state := disk.savedState
	if (disk.LastUsage == nil) != disk.LastSuccess.IsZero() {
		return savedState{}, errors.New("state has incomplete last-success data")
	}
	if disk.LastUsage != nil {
		if state.Accounts == nil {
			state.Accounts = make(map[string]accountState)
		}
		if _, exists := state.Accounts["account-1"]; !exists {
			state.Accounts["account-1"] = accountState{LastUsage: disk.LastUsage, LastSuccess: disk.LastSuccess}
		}
	}
	for id, account := range state.Accounts {
		if (account.LastUsage == nil) != account.LastSuccess.IsZero() {
			return savedState{}, fmt.Errorf("state has incomplete last-success data for %s", id)
		}
	}
	return state, nil
}

func saveState(path string, state savedState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
