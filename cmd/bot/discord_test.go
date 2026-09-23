package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeDiscord struct {
	mu                 sync.Mutex
	content            string
	posts              int
	patches            int
	getError           bool
	postErrorAfterSave bool
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (f *fakeDiscord) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bot test-token" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/users/@me":
		fmt.Fprint(w, `{"id":"99"}`)
	case r.URL.Path == "/channels/123/messages" && r.Method == http.MethodGet:
		if f.content == "" {
			fmt.Fprint(w, `[]`)
		} else {
			_ = json.NewEncoder(w).Encode([]discordMessage{{ID: "456", Content: f.content, Author: struct {
				ID string `json:"id"`
			}{ID: "99"}}})
		}
	case r.URL.Path == "/channels/123/messages/456" && r.Method == http.MethodGet:
		if f.getError {
			http.Error(w, "temporary error", http.StatusInternalServerError)
		} else if f.content == "" {
			http.NotFound(w, r)
		} else {
			_ = json.NewEncoder(w).Encode(discordMessage{ID: "456", Content: f.content, Author: struct {
				ID string `json:"id"`
			}{ID: "99"}})
		}
	case r.URL.Path == "/channels/123/messages" && r.Method == http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.content = body.Content
		f.posts++
		if f.postErrorAfterSave {
			http.Error(w, "temporary error", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"id":"456"}`)
	case r.URL.Path == "/channels/123/messages/456" && r.Method == http.MethodPatch:
		if f.content == "" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.content = body.Content
		f.patches++
		fmt.Fprint(w, `{"id":"456"}`)
	default:
		http.NotFound(w, r)
	}
}

func TestSingleDashboardAcrossRestartAndFailure(t *testing.T) {
	fake := &fakeDiscord{}
	d := newDiscordClient("test-token", "123")
	d.baseURL = "https://discord.test"
	d.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		fake.serve(recorder, r)
		return recorder.Result(), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	state := savedState{}
	accounts := []accountConfig{{ID: "account-1", Name: "Account 1"}}
	first := renderDashboard(accounts, nil, time.Unix(1800000000, 0))
	fake.postErrorAfterSave = true
	if err := d.publish(ctx, &state, path, first); err == nil {
		t.Fatal("expected ambiguous create failure")
	}
	if state.MessageID != "" || fake.posts != 1 {
		t.Fatalf("ambiguous create should leave ID unknown: %+v", state)
	}
	fake.postErrorAfterSave = false
	if err := d.publish(ctx, &state, path, first); err != nil {
		t.Fatal(err)
	}
	if state.MessageID != "456" || fake.posts != 1 {
		t.Fatalf("first publish: state=%+v posts=%d", state, fake.posts)
	}
	state, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &usageSnapshot{FiveHour: usageWindow{82, 1900000000}, Weekly: usageWindow{61, 2000000000}}
	states := map[string]accountState{"account-1": {LastUsage: snapshot, LastSuccess: time.Unix(1800000000, 0).UTC()}}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	if err := d.publish(ctx, &state, path, renderDashboard(accounts, states, time.Unix(1800000000, 0))); err != nil {
		t.Fatal(err)
	}
	if fake.posts != 1 || fake.patches != 2 || !strings.Contains(fake.content, "### Account 1\n> 🟢 **讀取正常**") {
		t.Fatalf("update created another message: %+v", fake)
	}
	fake.getError = true
	failedState := states["account-1"]
	failedState.Failed = true
	states["account-1"] = failedState
	if err := d.publish(ctx, &state, path, renderDashboard(accounts, states, time.Unix(1800000000, 0))); err == nil {
		t.Fatal("expected temporary GET failure")
	}
	if fake.posts != 1 {
		t.Fatal("temporary failure created another message")
	}
	fake.getError = false
	if err := d.publish(ctx, &state, path, renderDashboard(accounts, states, time.Unix(1800000000, 0))); err != nil {
		t.Fatal(err)
	}
	if fake.posts != 1 || fake.patches != 3 || !strings.Contains(fake.content, "### Account 1\n> 🔴 **讀取失敗**") {
		t.Fatalf("failed state did not preserve and edit message: %+v", fake)
	}
	state.MessageID = "" // Simulate a lost message ID while the dashboard remains.
	if err := d.publish(ctx, &state, path, first); err != nil {
		t.Fatal(err)
	}
	if fake.posts != 1 || state.MessageID != "456" {
		t.Fatalf("history recovery duplicated dashboard: %+v", fake)
	}
	fake.content = "1. **Account 1** ⚪\n  - **5-Hour**   —\n  - **Weekly**   —"
	state.MessageID = ""
	if err := d.publish(ctx, &state, path, first); err != nil {
		t.Fatal(err)
	}
	if fake.posts != 1 || state.MessageID != "456" || fake.content != first {
		t.Fatalf("previous unheaded dashboard was not edited in place: %+v", fake)
	}
	fake.content = dashboardTitle + "\n\nlegacy account"
	state.MessageID = ""
	if err := d.publish(ctx, &state, path, first); err != nil {
		t.Fatal(err)
	}
	if fake.posts != 1 || state.MessageID != "456" || fake.content != first {
		t.Fatalf("legacy dashboard was not edited in place: %+v", fake)
	}
}

func TestIsDashboardAcceptsCurrentAndLegacyFormats(t *testing.T) {
	current := renderDashboard([]accountConfig{{ID: "one", Name: "Personal"}}, nil, time.Unix(1800000000, 0))
	msg := discordMessage{Content: current}
	msg.Author.ID = "99"
	unrelated := discordMessage{Content: "1. **Personal** ⚪\n  - unrelated"}
	unrelated.Author.ID = "99"
	if !isDashboard(msg, "99") || isDashboard(msg, "another-bot") ||
		isDashboard(unrelated, "99") {
		t.Fatal("current dashboard identification failed")
	}
	msg = discordMessage{Content: "1. **Personal** ⚪\n  - **5-Hour**   —\n  - **Weekly**   —"}
	msg.Author.ID = "99"
	if !isDashboard(msg, "99") {
		t.Fatal("previous unheaded dashboard was not recognized")
	}
	for _, heading := range []string{legacyDashboardHeading, dashboardTitle} {
		msg := discordMessage{Content: heading + "\n\naccount"}
		msg.Author.ID = "99"
		if !isDashboard(msg, "99") || isDashboard(msg, "another-bot") {
			t.Fatalf("unexpected dashboard identification for %q", heading)
		}
	}
	msg = discordMessage{Content: dashboardHeading + "\n\nnot an account"}
	msg.Author.ID = "99"
	if isDashboard(msg, "99") {
		t.Fatal("new heading without an account was accepted")
	}
}

func TestDashboardTooLongIsNotPublished(t *testing.T) {
	d := newDiscordClient("test-token", "123")
	state := savedState{}
	for _, content := range []string{strings.Repeat("測", 2001), strings.Repeat("😀", 1001)} {
		err := d.publish(context.Background(), &state, filepath.Join(t.TempDir(), "state.json"), content)
		if err == nil || state.MessageID != "" {
			t.Fatalf("overlong dashboard was accepted: %v", err)
		}
	}
}
