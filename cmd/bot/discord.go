package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type discordClient struct {
	baseURL   string
	token     string
	channelID string
	botID     string
	http      *http.Client
}

type discordMessage struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Author  struct {
		ID string `json:"id"`
	} `json:"author"`
}

func newDiscordClient(token, channelID string) *discordClient {
	return &discordClient{
		baseURL: "https://discord.com/api/v10", token: token, channelID: channelID,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (d *discordClient) request(ctx context.Context, method, path string, payload any) ([]byte, int, error) {
	var encoded []byte
	if payload != nil {
		var err error
		encoded, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, body)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bot "+d.token)
		req.Header.Set("User-Agent", "CodexRatelimiteBot/1.0")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := d.http.Do(req)
		if err != nil {
			return nil, 0, err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, 0, readErr
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			var rate struct {
				RetryAfter float64 `json:"retry_after"`
			}
			_ = json.Unmarshal(data, &rate)
			delay := time.Duration(rate.RetryAfter * float64(time.Second))
			if delay <= 0 {
				delay = time.Second
			}
			if delay > 30*time.Second {
				return nil, resp.StatusCode, errors.New("Discord rate-limit retry exceeds 30 seconds")
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, 0, ctx.Err()
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return data, resp.StatusCode, fmt.Errorf("Discord HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, errors.New("Discord retry limit reached")
}

func (d *discordClient) authenticate(ctx context.Context) error {
	data, _, err := d.request(ctx, http.MethodGet, "/users/@me", nil)
	if err != nil {
		return err
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &user); err != nil {
		return err
	}
	if user.ID == "" {
		return errors.New("Discord did not return a bot user ID")
	}
	d.botID = user.ID
	return nil
}

func (d *discordClient) messagePath(id string) string {
	return "/channels/" + d.channelID + "/messages/" + id
}

func (d *discordClient) getMessage(ctx context.Context, id string) (discordMessage, bool, error) {
	data, status, err := d.request(ctx, http.MethodGet, d.messagePath(id), nil)
	if status == http.StatusNotFound {
		return discordMessage{}, false, nil
	}
	if err != nil {
		return discordMessage{}, false, err
	}
	var msg discordMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return discordMessage{}, false, err
	}
	return msg, true, nil
}

func isDashboard(msg discordMessage, botID string) bool {
	return msg.Author.ID == botID && strings.HasPrefix(msg.Content, dashboardTitle+"\n\n")
}

// findDashboard pages through the channel until it reaches the beginning.
// A search error must not be treated as absence: creating then could duplicate.
func (d *discordClient) findDashboard(ctx context.Context) (string, error) {
	before := ""
	for {
		path := "/channels/" + d.channelID + "/messages?limit=100"
		if before != "" {
			path += "&before=" + url.QueryEscape(before)
		}
		data, _, err := d.request(ctx, http.MethodGet, path, nil)
		if err != nil {
			return "", err
		}
		var messages []discordMessage
		if err := json.Unmarshal(data, &messages); err != nil {
			return "", err
		}
		for _, msg := range messages {
			if isDashboard(msg, d.botID) {
				return msg.ID, nil
			}
		}
		if len(messages) < 100 {
			return "", nil
		}
		before = messages[len(messages)-1].ID
	}
}

func (d *discordClient) createMessage(ctx context.Context, content string) (string, error) {
	payload := map[string]any{"content": content, "allowed_mentions": map[string]any{"parse": []string{}}}
	data, _, err := d.request(ctx, http.MethodPost, "/channels/"+d.channelID+"/messages", payload)
	if err != nil {
		return "", err
	}
	var msg discordMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", err
	}
	if msg.ID == "" {
		return "", errors.New("Discord create response has no message ID")
	}
	return msg.ID, nil
}

func (d *discordClient) editMessage(ctx context.Context, id, content string) (bool, error) {
	payload := map[string]any{"content": content, "allowed_mentions": map[string]any{"parse": []string{}}}
	_, status, err := d.request(ctx, http.MethodPatch, d.messagePath(id), payload)
	if status == http.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

func (d *discordClient) publish(ctx context.Context, state *savedState, statePath, content string) error {
	if len([]rune(content)) > 2000 {
		return errors.New("dashboard exceeds Discord's 2000-character limit")
	}
	if state.MessageID != "" {
		msg, found, err := d.getMessage(ctx, state.MessageID)
		if err != nil {
			return err
		}
		if found && isDashboard(msg, d.botID) {
			if msg.Content == content {
				return nil
			}
			updated, err := d.editMessage(ctx, state.MessageID, content)
			if err != nil {
				return err
			}
			if updated {
				return nil
			}
		}
		state.MessageID = ""
		if err := saveState(statePath, *state); err != nil {
			return err
		}
	}

	id, err := d.findDashboard(ctx)
	if err != nil {
		return err
	}
	if id != "" {
		state.MessageID = id
		if err := saveState(statePath, *state); err != nil {
			return err
		}
		updated, err := d.editMessage(ctx, id, content)
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("recovered Discord dashboard disappeared before editing")
		}
		return nil
	}
	id, err = d.createMessage(ctx, content)
	if err != nil {
		return err
	}
	state.MessageID = id
	return saveState(statePath, *state)
}

func validSnowflake(id string) bool {
	if id == "" {
		return false
	}
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}
