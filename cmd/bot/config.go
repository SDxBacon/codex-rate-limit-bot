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
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Home        string          `json:"home"`
	Type        accountType     `json:"type,omitempty"`
	HelloModel  helloModel      `json:"hello_model,omitempty"`
	HelloEffort json.RawMessage `json:"hello_effort,omitempty"`
}

type helloModel string

func (m *helloModel) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) != value || value == "" ||
		strings.ContainsAny(value, "\r\n\t") || len(value) > 200 {
		return errors.New("hello_model must be a nonempty model name")
	}
	*m = helloModel(value)
	return nil
}

type helloOptions struct {
	Model  string
	Effort *string
}

func (a accountConfig) claudeHelloOptions() (helloOptions, error) {
	options := helloOptions{Model: string(a.HelloModel)}
	if options.Model == "" {
		options.Model = "sonnet"
	}
	effort := "low"
	if len(a.HelloEffort) > 0 {
		if string(a.HelloEffort) == "null" {
			return options, nil
		}
		if err := json.Unmarshal(a.HelloEffort, &effort); err != nil {
			return options, errors.New("hello_effort must be an effort name or null")
		}
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		options.Effort = &effort
		return options, nil
	default:
		return options, errors.New("invalid hello_effort")
	}
}

type accountType string

func (t *accountType) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || (value != accountCodex && value != accountClaude) {
		return errors.New("account type must be codex or claude")
	}
	*t = accountType(value)
	return nil
}

const (
	accountCodex  = "codex"
	accountClaude = "claude"
)

func (a accountConfig) providerType() string {
	if a.Type == "" {
		return accountCodex
	}
	return string(a.Type)
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
		if account.providerType() != accountClaude && (account.HelloModel != "" || len(account.HelloEffort) > 0) {
			return appConfig{}, fmt.Errorf("account %d: hello settings require type claude", i+1)
		}
		if _, err := account.claudeHelloOptions(); err != nil {
			return appConfig{}, fmt.Errorf("account %d: %w", i+1, err)
		}
		if kind := account.providerType(); kind != accountCodex && kind != accountClaude {
			return appConfig{}, fmt.Errorf("account %d has invalid type", i+1)
		}
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
