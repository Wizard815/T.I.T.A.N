// Command orproxy stands in for Argos in the demo: it maps T.I.T.A.N's
// model names to OpenRouter models, and stops answering once the demo has
// spent its budget (counted from OpenRouter's own usage.cost).
//
//	ORPROXY_ADDR     default 127.0.0.1:9920
//	ORPROXY_BUDGET   US dollars, default 0.90
//	ORPROXY_MODELS   "titan-guard=google/gemini-2.5-flash-lite titan-agent=... titan-fallback=..."
//	OPENROUTER_API_KEY  sent as the bearer token
//
// GET /spend shows what has been spent and every call.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type call struct {
	At       time.Time `json:"at"`
	Asked    string    `json:"asked"`
	Model    string    `json:"model"`
	In       int       `json:"prompt_tokens"`
	Out      int       `json:"completion_tokens"`
	Cost     float64   `json:"cost_usd"`
	MS       int64     `json:"ms"`
	Status   int       `json:"status"`
	ToolCall bool      `json:"tool_call"`
}

var (
	mu    sync.Mutex
	spent float64
	calls []call
)

func main() {
	addr := env("ORPROXY_ADDR", "127.0.0.1:9920")
	budget, _ := strconv.ParseFloat(env("ORPROXY_BUDGET", "0.90"), 64)
	models := map[string]string{}
	for _, kv := range strings.Fields(os.Getenv("ORPROXY_MODELS")) {
		k, v, _ := strings.Cut(kv, "=")
		models[k] = v
	}
	key := env("OPENROUTER_API_KEY", "sk-or-placeholder")
	hc := &http.Client{Timeout: 120 * time.Second}

	http.HandleFunc("GET /spend", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"spent_usd": spent, "budget_usd": budget, "calls": calls})
	})
	http.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body) != nil {
			http.Error(w, `{"error":{"message":"bad json"}}`, 400)
			return
		}
		asked, _ := body["model"].(string)
		real, ok := models[asked]
		if !ok {
			http.Error(w, `{"error":{"message":"no such model"}}`, 404)
			return
		}
		mu.Lock()
		over := spent >= budget
		mu.Unlock()
		if over {
			w.WriteHeader(429)
			fmt.Fprintf(w, `{"error":{"message":"demo budget of $%.2f is spent"}}`, budget)
			return
		}
		body["model"] = real
		body["usage"] = map[string]any{"include": true}
		if _, has := body["max_tokens"]; !has {
			body["max_tokens"] = 600
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(r.Context(), "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Title", "titan-demo")
		start := time.Now()
		resp, err := hc.Do(req)
		if err != nil {
			http.Error(w, `{"error":{"message":"openrouter unreachable"}}`, 502)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		var u struct {
			Choices []struct {
				Message struct {
					ToolCalls []any `json:"tool_calls"`
				}
			}
			Usage struct {
				PromptTokens     int     `json:"prompt_tokens"`
				CompletionTokens int     `json:"completion_tokens"`
				Cost             float64 `json:"cost"`
			}
		}
		json.Unmarshal(out, &u)
		c := call{At: start, Asked: asked, Model: real, In: u.Usage.PromptTokens, Out: u.Usage.CompletionTokens, Cost: u.Usage.Cost, MS: time.Since(start).Milliseconds(), Status: resp.StatusCode}
		c.ToolCall = len(u.Choices) > 0 && len(u.Choices[0].Message.ToolCalls) > 0
		mu.Lock()
		spent += c.Cost
		calls = append(calls, c)
		total := spent
		mu.Unlock()
		log.Printf("%s -> %s %d in/%d out $%.6f (total $%.4f) %dms", asked, real, c.In, c.Out, c.Cost, total, c.MS)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(out)
	})
	log.Printf("orproxy on %s, budget $%.2f, models %v", addr, budget, models)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
