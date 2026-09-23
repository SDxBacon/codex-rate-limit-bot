package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type accountConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Home string `json:"home"`
}

type appConfig struct {
	Accounts []accountConfig `json:"accounts"`
}

var accountIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func loadConfig(path string) (appConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return appConfig{}, err
	}
	var config appConfig
	if err := json.Unmarshal(b, &config); err != nil {
		return appConfig{}, fmt.Errorf("decode config: %w", err)
	}
	if len(config.Accounts) == 0 {
		return appConfig{}, errors.New("config must contain at least one account")
	}
	ids, homes := make(map[string]bool), make(map[string]bool)
	for i, account := range config.Accounts {
		if !accountIDPattern.MatchString(account.ID) || ids[account.ID] {
			return appConfig{}, fmt.Errorf("account %d has invalid or duplicate id", i+1)
		}
		if account.Name != strings.TrimSpace(account.Name) || account.Name == "" || len([]rune(account.Name)) > 32 ||
			strings.ContainsAny(account.Name, "\r\n\t") {
			return appConfig{}, fmt.Errorf("account %d has invalid name", i+1)
		}
		home := filepath.Clean(account.Home)
		if account.Home == "" || filepath.IsAbs(account.Home) || home == "." || home == ".." ||
			strings.HasPrefix(home, ".."+string(filepath.Separator)) || home != account.Home || homes[home] {
			return appConfig{}, fmt.Errorf("account %d has invalid or duplicate home", i+1)
		}
		ids[account.ID], homes[home] = true, true
	}
	return config, nil
}

func reloadConfig(path string, previous appConfig) (appConfig, error) {
	config, err := loadConfig(path)
	if err != nil {
		return previous, err
	}
	return config, nil
}
