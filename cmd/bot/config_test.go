package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndReloadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	good := `{"accounts":[{"id":"second","name":"Personal","home":"second"},{"id":"first","name":"Work","home":"first"}]}`
	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil || config.Accounts[0].ID != "second" || config.Accounts[1].ID != "first" {
		t.Fatalf("unexpected config: %+v, %v", config, err)
	}
	if err := os.WriteFile(path, []byte(`{"accounts":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	previous, err := reloadConfig(path, config)
	if err == nil || previous.Accounts[0].ID != "second" {
		t.Fatalf("invalid reload should retain prior config: %+v, %v", previous, err)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, raw := range []string{
		`{"accounts":[]}`,
		`{"accounts":[{"id":"one","name":"One","home":"one","type":"other"}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"one","type":""}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"one","type":null}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"one","type":1}]}`,
		`{"accounts":[{"id":"bad id","name":"One","home":"one"}]}`,
		`{"accounts":[{"id":"one","name":"","home":"one"}]}`,
		`{"accounts":[{"id":"one","name":"Line\nBreak","home":"one"}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"../one"}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"/tmp/one"}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"one"},{"id":"one","name":"Two","home":"two"}]}`,
		`{"accounts":[{"id":"one","name":"One","home":"one"},{"id":"two","name":"Two","home":"one"}]}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err == nil {
			t.Fatalf("accepted invalid config: %s", raw)
		}
	}
}

func TestMixedConfigAndInvalidTypeReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"accounts":[{"id":"old","name":"Old","home":"old"},{"id":"codex","name":"Codex","home":"codex","type":"codex"},{"id":"claude","name":"Claude","home":"claude","type":"claude"}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Accounts[0].providerType() != accountCodex || config.Accounts[1].providerType() != accountCodex || config.Accounts[2].providerType() != accountClaude {
		t.Fatal(config)
	}
	if err := os.WriteFile(path, []byte(`{"accounts":[{"id":"one","name":"One","home":"one","type":"unknown"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	retained, err := reloadConfig(path, config)
	if err == nil || len(retained.Accounts) != 3 || retained.Accounts[2].Type != accountClaude {
		t.Fatal("invalid type replaced valid config")
	}
}
