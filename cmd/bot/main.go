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

type usageProbe func(context.Context, string, string) (usageSnapshot, error)

type accountProvider struct {
	Binary string
	Probe  usageProbe
	Hello  helloRunner
}

func probeAccounts(ctx context.Context, accounts []accountConfig, root string, states map[string]accountState,
	providers map[string]accountProvider, now func() time.Time, onAttempt func()) {
	configured := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		configured[account.ID] = true
		home := filepath.Join(root, account.Home)
		kind := account.providerType()
		provider := providers[kind]
		state := states[account.ID]
		oldType := state.Type
		if oldType == "" {
			oldType = accountCodex
		}
		if state.Home != home || oldType != kind {
			state = accountState{Home: home}
		}
		state.Type = kind
		state.expireHello(now().UTC())
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		snapshot, err := provider.Probe(probeCtx, provider.Binary, home)
		cancel()
		if err != nil {
			state.Timer = timerUnknown
			state.Failed = true
			states[account.ID] = state
			log.Printf("%s usage read failed for %s: %v", kind, account.ID, err)
			continue
		}
		readAt := now().UTC()
		state.recordUsage(snapshot, readAt)
		states[account.ID] = state
		if kind != accountCodex || provider.Hello == nil || !state.shouldSendHello(readAt) {
			continue
		}
		attemptAt := now().UTC()
		state.LastAttempt = attemptAt
		state.HelloAt = attemptAt
		state.HelloReset = *snapshot.FiveHour.ResetsAt
		state.Timer = timerUnknown
		states[account.ID] = state
		helloCtx, helloCancel := context.WithTimeout(ctx, helloTimeout)
		err = provider.Hello(helloCtx, provider.Binary, home)
		helloCancel()
		if err != nil {
			log.Printf("Codex hello failed for %s: %v", account.ID, err)
		}
		// This read has its own deadline, regardless of how long hello took.
		recheckCtx, recheckCancel := context.WithTimeout(ctx, probeTimeout)
		recheck, err := provider.Probe(recheckCtx, provider.Binary, home)
		recheckCancel()
		if err != nil {
			state.Failed = true
			log.Printf("Codex usage recheck failed for %s: %v", account.ID, err)
		} else {
			recheckAt := now().UTC()
			dr := time.Duration(*recheck.FiveHour.ResetsAt-*snapshot.FiveHour.ResetsAt) * time.Second
			state.recordUsage(recheck, recheckAt)
			if *recheck.FiveHour.UsedPercent == 0 {
				state.Timer = timerUnknown
			} else if state.Timer == timerUnknown &&
				recheckAt.Before(time.Unix(*recheck.FiveHour.ResetsAt, 0)) &&
				near(dr, 0) && !(dr >= timerTolerance && near(dr, recheckAt.Sub(readAt))) {
				state.Timer = timerActive
			}
		}
		states[account.ID] = state
		if onAttempt != nil {
			onAttempt()
		}
	}
	for id := range states {
		if !configured[id] {
			delete(states, id)
		}
	}
}

func runCycle(ctx context.Context, d *discordClient, disk *savedState, statePath, accountRoot string,
	config appConfig, states map[string]accountState, providers map[string]accountProvider) {
	publish := func() {
		content := renderDashboard(config.Accounts, states, time.Now())
		postCtx, postCancel := context.WithTimeout(ctx, 60*time.Second)
		defer postCancel()
		if err := d.publish(postCtx, disk, statePath, content); err != nil {
			log.Printf("Discord dashboard update failed: %v", err)
		}
	}
	probeAccounts(ctx, config.Accounts, accountRoot, states, providers, time.Now, publish)
	publish()
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
	claudeBinary := os.Getenv("CLAUDE_BINARY")
	if claudeBinary == "" {
		claudeBinary = "claude"
	}
	providers := map[string]accountProvider{
		accountCodex:  {Binary: binary, Probe: probeUsage, Hello: sendHello},
		accountClaude: {Binary: claudeBinary, Probe: probeClaudeUsage},
	}
	// Rewrite legacy state once, removing persisted usage while retaining the ID.
	if err := saveState(statePath, state); err != nil {
		return fmt.Errorf("normalize state: %w", err)
	}
	states := make(map[string]accountState)
	runCycle(ctx, d, &state, statePath, accountRoot, config, states, providers)
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
			runCycle(ctx, d, &state, statePath, accountRoot, config, states, providers)
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
