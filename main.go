package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const pollInterval = 5 * time.Minute

func runCycle(ctx context.Context, d *discordClient, state *savedState, statePath, codexBinary, codexHome string) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	snapshot, probeErr := probeUsage(probeCtx, codexBinary, codexHome)
	cancel()
	if probeErr == nil {
		state.LastUsage = &snapshot
		state.LastSuccess = time.Now().UTC()
		if err := saveState(statePath, *state); err != nil {
			log.Printf("save last successful usage: %v", err)
			return
		}
	} else {
		log.Printf("Codex usage read failed: %v", probeErr)
	}
	content := renderDashboard(state.LastUsage, state.LastSuccess, probeErr != nil)
	postCtx, postCancel := context.WithTimeout(ctx, 60*time.Second)
	defer postCancel()
	if err := d.publish(postCtx, state, statePath, content); err != nil {
		log.Printf("Discord dashboard update failed: %v", err)
	}
}

func run(ctx context.Context) error {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	channelID := os.Getenv("DISCORD_CHANNEL_ID")
	codexHome := os.Getenv("CODEX_HOME")
	statePath := os.Getenv("STATE_PATH")
	if statePath == "" {
		statePath = "/data/state.json"
	}
	if token == "" || !validSnowflake(channelID) || codexHome == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN, numeric DISCORD_CHANNEL_ID, and CODEX_HOME are required")
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
	codexBinary := os.Getenv("CODEX_BINARY")
	if codexBinary == "" {
		codexBinary = "codex"
	}
	runCycle(ctx, d, &state, statePath, codexBinary, codexHome)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			runCycle(ctx, d, &state, statePath, codexBinary, codexHome)
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
