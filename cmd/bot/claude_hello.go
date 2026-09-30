package main

import (
	"context"
	"encoding/json"
	"errors"
)

func sendClaudeHello(ctx context.Context, binary, home string, options helloOptions) error {
	return withClaudeSession(ctx, binary, home, claudeSessionOptions{Hello: &options}, func(session *claudeSession) error {
		init, err := session.request("initialize", false)
		if err != nil {
			return err
		}
		settings, err := session.request("get_settings", false)
		if err != nil {
			return err
		}
		if err := verifyClaudeHelloSettings(init, settings, options); err != nil {
			return err
		}
		session.stage = "hello"
		if err := session.send(map[string]any{
			"type": "user", "message": map[string]string{"role": "user", "content": "hello"},
			"parent_tool_use_id": nil, "session_id": "",
		}); err != nil {
			return err
		}
		for {
			frame, err := session.next()
			if err != nil {
				return err
			}
			if string(frame["type"]) != `"result"` {
				continue
			}
			var subtype string
			var isError *bool
			if json.Unmarshal(frame["subtype"], &subtype) != nil ||
				json.Unmarshal(frame["is_error"], &isError) != nil || isError == nil {
				return errors.New("invalid_result")
			}
			if subtype != "success" || *isError {
				return errors.New("hello_failed")
			}
			return nil
		}
	})
}

func verifyClaudeHelloSettings(init, settings json.RawMessage, options helloOptions) error {
	var models struct {
		Models []struct {
			Value         string `json:"value"`
			ResolvedModel string `json:"resolvedModel"`
		} `json:"models"`
	}
	var applied struct {
		Applied *struct {
			Model  *string `json:"model"`
			Effort *string `json:"effort"`
		} `json:"applied"`
	}
	if json.Unmarshal(init, &models) != nil || json.Unmarshal(settings, &applied) != nil || applied.Applied == nil {
		return errors.New("invalid_settings")
	}
	expected := options.Model
	for _, model := range models.Models {
		if model.Value == options.Model && model.ResolvedModel != "" {
			expected = model.ResolvedModel
			break
		}
	}
	if expected == "" || applied.Applied.Model == nil || *applied.Applied.Model != expected {
		return errors.New("model_not_applied")
	}
	if options.Effort != nil && (applied.Applied.Effort == nil || *applied.Applied.Effort != *options.Effort) {
		return errors.New("effort_not_applied")
	}
	return nil
}
