package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is an OpenAI-style chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string  `json:"type"`
	Function ToolDef `json:"function"`
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Models calls Argos's OpenAI-compatible API. Which real model (local or
// cloud) each name means is decided in Argos.
type Models struct {
	URL     string // e.g. http://argos:8080/v1
	APIKey  string
	HTTP    *http.Client
	Timeout time.Duration
}

var ErrNoModels = errors.New("no model is set up (argos.url in titan.yaml)")

// Chat asks one model. Errors say which model failed and why.
func (m *Models) Chat(ctx context.Context, model string, msgs []Message, tools []Tool, jsonOut bool) (Message, error) {
	if m == nil || m.URL == "" {
		return Message{}, ErrNoModels
	}
	if model == "" {
		return Message{}, errors.New("no model name given")
	}
	body := map[string]any{"model": model, "messages": msgs, "temperature": 0, "stream": false}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if jsonOut {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	if m.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.Timeout)
		defer cancel()
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(m.URL, "/")+"/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Argos-App", "titan") // Argos counts usage and applies rules per app
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	hc := m.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("%s: Argos isn't answering: %v", model, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return Message{}, fmt.Errorf("%s: Argos answered %d: %s", model, resp.StatusCode, trim(string(rb), 200))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &out); err != nil || len(out.Choices) == 0 {
		return Message{}, fmt.Errorf("%s: Argos's answer had no message", model)
	}
	return out.Choices[0].Message, nil
}

// First tries each model in order and returns the first answer, with the
// model that gave it.
func (m *Models) First(ctx context.Context, models []string, msgs []Message, tools []Tool, jsonOut bool) (Message, string, error) {
	var errs []string
	for _, name := range models {
		if name == "" {
			continue
		}
		msg, err := m.Chat(ctx, name, msgs, tools, jsonOut)
		if err == nil {
			return msg, name, nil
		}
		errs = append(errs, err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return Message{}, "", ErrNoModels
	}
	return Message{}, "", errors.New(strings.Join(errs, "; "))
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
