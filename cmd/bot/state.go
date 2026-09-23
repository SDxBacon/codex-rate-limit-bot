package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type savedState struct {
	MessageID string `json:"message_id"`
}

func loadState(path string) (savedState, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return savedState{}, nil
	}
	if err != nil {
		return savedState{}, err
	}
	// Legacy usage data is ignored: a restart begins with no timer evidence.
	var state savedState
	if err := json.Unmarshal(b, &state); err != nil {
		return savedState{}, fmt.Errorf("decode state: %w", err)
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
