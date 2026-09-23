package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const pollInterval = 5 * time.Minute
const defaultStateSaveInterval = 30 * time.Minute

type usageProbe func(context.Context, string, string) (usageSnapshot, error)

func parseStateSaveInterval(raw string) (time.Duration, error) {
	if raw == "" {
		return defaultStateSaveInterval, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("STATE_SAVE_INTERVAL must be a positive Go duration")
	}
	return duration, nil
}

func probeAccounts(ctx context.Context, accounts []accountConfig, root, binary string, state *savedState, probe usageProbe, now func() time.Time) (map[string]bool, bool) {
	failed := make(map[string]bool)
	anySuccess := false
	if state.Accounts == nil {
		state.Accounts = make(map[string]accountState)
	}
	for _, account := range accounts {
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		snapshot, err := probe(probeCtx, binary, filepath.Join(root, account.Home))
		cancel()
		if err != nil {
			failed[account.ID] = true
			log.Printf("Codex usage read failed for %s: %v", account.ID, err)
			continue
		}
		state.Accounts[account.ID] = accountState{LastUsage: &snapshot, LastSuccess: now().UTC()}
		anySuccess = true
	}
	return failed, anySuccess
}

func persistUsageIfDue(statePath string, state savedState, now time.Time, lastSave *time.Time, interval time.Duration, anySuccess bool) {
	if !anySuccess || (!lastSave.IsZero() && now.Sub(*lastSave) < interval) {
		return
	}
	if err := saveState(statePath, state); err != nil {
		log.Printf("save last successful usage: %v", err)
		return
	}
	*lastSave = now
}

func runCycle(ctx context.Context, d *discordClient, state *savedState, statePath, binary, accountRoot string, config appConfig, interval time.Duration, lastSave *time.Time, probe usageProbe) {
	failed, anySuccess := probeAccounts(ctx, config.Accounts, accountRoot, binary, state, probe, time.Now)
	persistUsageIfDue(statePath, *state, time.Now(), lastSave, interval, anySuccess)
	content := renderDashboard(config.Accounts, state.Accounts, failed)
	postCtx, postCancel := context.WithTimeout(ctx, 60*time.Second)
	defer postCancel()
	if err := d.publish(postCtx, state, statePath, content); err != nil {
		log.Printf("Discord dashboard update failed: %v", err)
	}
}

func run(ctx context.Context) error {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	channelID := os.Getenv("DISCORD_CHANNEL_ID")
	accountRoot := os.Getenv("ACCOUNTS_ROOT")
	configPath := os.Getenv("CONFIG_PATH")
	statePath := os.Getenv("STATE_PATH")
	if statePath == "" {
		statePath = "/data/state.json"
	}
	if configPath == "" {
		configPath = "/config/config.json"
	}
	if token == "" || !validSnowflake(channelID) || accountRoot == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN, numeric DISCORD_CHANNEL_ID, and ACCOUNTS_ROOT are required")
	}
	interval, err := parseStateSaveInterval(os.Getenv("STATE_SAVE_INTERVAL"))
	if err != nil {
		return err
	}
	config, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	state, err := loadState(statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	d := newDiscordClient(token, channelID)
	authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = d.authenticate(authCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("Discord authentication: %w", err)
	}
	binary := os.Getenv("CODEX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	var lastSave time.Time
	runCycle(ctx, d, &state, statePath, binary, accountRoot, config, interval, &lastSave, probeUsage)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			var reloadErr error
			config, reloadErr = reloadConfig(configPath, config)
			if reloadErr != nil {
				log.Printf("reload config failed; using previous config: %v", reloadErr)
			}
			runCycle(ctx, d, &state, statePath, binary, accountRoot, config, interval, &lastSave, probeUsage)
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}
