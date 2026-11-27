// Command fakeworld stands in for the homelab so T.I.T.A.N can be tried
// without touching anything real: Argos (a scripted guard and helper),
// Docker (over a unix socket), Uptime Kuma, FAITH's health check and a
// DnD sync that has gone stale.
//
//	go run ./demo/fakeworld -addr :9900 -sock /tmp/fake-docker.sock
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wizard815/titan/internal/fakes"
)

var (
	mu      sync.Mutex
	running = map[string]bool{"faith": true, "minecraft": false, "valheim": true, "hermes-sam": true, "cloudflared": false}
	synced  = time.Now().Add(-30 * time.Hour)
)

func main() {
	addr := flag.String("addr", ":9900", "HTTP address for Argos, Kuma, FAITH and the sync job")
	sock := flag.String("sock", "/tmp/fake-docker.sock", "unix socket that acts as Docker")
	flag.String("amp", "127.0.0.1:9901", "address for the fake AMP")
	flag.Parse()

	os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		log.Fatal(err)
	}
	go http.Serve(ln, http.HandlerFunc(docker))

	amp := fakes.NewAMP("titan", "demo-amp", "Enshrouded01")
	amp.Running["Enshrouded01"] = false
	ampAddr := flag.Lookup("amp").Value.String()
	go func() { log.Fatal(http.ListenAndServe(ampAddr, amp)) }()

	m := http.NewServeMux()
	m.HandleFunc("GET /tautulli/api/v2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"data":{"stream_count":"2","stream_count_transcode":1}}}`))
	})
	m.HandleFunc("GET /seerr/api/v1/request/count", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pending":1,"processing":2}`))
	})
	m.HandleFunc("GET /sonarr/api/v3/queue", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"totalRecords":3}`)) })
	m.HandleFunc("GET /argos/running", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running":[{"model":"qwen3-8b","state":"ready"}]}`))
	})
	m.HandleFunc("GET /plex/identity", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		up := running["cloudflared"]
		mu.Unlock()
		if !up {
			w.WriteHeader(502)
		}
	})
	m.HandleFunc("POST /v1/chat/completions", argos)
	m.HandleFunc("GET /kuma/api/status-page/home", func(w http.ResponseWriter, r *http.Request) {
		j(w, map[string]any{"publicGroupList": []any{map[string]any{"name": "Media", "monitorList": []any{map[string]any{"id": 1, "name": "Jellyfin"}, map[string]any{"id": 2, "name": "Immich"}}}}})
	})
	m.HandleFunc("GET /kuma/api/status-page/heartbeat/home", func(w http.ResponseWriter, r *http.Request) {
		j(w, map[string]any{"heartbeatList": map[string]any{
			"1": []any{map[string]any{"status": 1, "ping": 23.0}},
			"2": []any{map[string]any{"status": 0, "msg": "connect ECONNREFUSED"}},
		}})
	})
	m.HandleFunc("GET /faith/health", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		up := running["faith"]
		mu.Unlock()
		if !up {
			w.WriteHeader(502)
		}
		w.Write([]byte("ok"))
	})
	m.HandleFunc("GET /dnd/status", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		j(w, map[string]any{"last_sync": synced.Format(time.RFC3339)})
	})
	m.HandleFunc("POST /dnd/sync", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		synced = time.Now()
		mu.Unlock()
		w.WriteHeader(202)
	})
	log.Printf("fakeworld on %s, docker at %s", *addr, *sock)
	log.Fatal(http.ListenAndServe(*addr, m))
}

func j(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func docker(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "containers" {
		w.WriteHeader(404)
		return
	}
	name, op := parts[1], parts[2]
	mu.Lock()
	defer mu.Unlock()
	run, ok := running[name]
	if !ok {
		w.WriteHeader(404)
		j(w, map[string]string{"message": "No such container: " + name})
		return
	}
	switch op {
	case "json":
		st := map[bool]string{true: "running", false: "exited"}[run]
		j(w, map[string]any{"RestartCount": 0, "State": map[string]any{"Status": st, "Running": run, "StartedAt": time.Now().Add(-26 * time.Hour).Format(time.RFC3339)}})
	case "logs":
		for _, l := range []string{"[server] Loading world", "[server] ERROR Out of memory, java.lang.OutOfMemoryError", "[auth] rcon password=hunter22 rejected", "[server] Stopping"} {
			h := make([]byte, 8)
			h[0] = 1
			binary.BigEndian.PutUint32(h[4:], uint32(len(l)+1))
			w.Write(h)
			w.Write([]byte(l + "\n"))
		}
	case "start", "restart":
		running[name] = true
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}

// argos is a scripted stand-in: the guard refuses obvious harm, and the
// helper reads status, then offers the first fix that fits.
func argos(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string
		Messages []struct {
			Role, Content string
		}
	}
	json.NewDecoder(r.Body).Decode(&body)
	msgs := body.Messages
	last := msgs[len(msgs)-1]
	say := func(s string) {
		j(w, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": s}}}})
	}
	call := func(name, args string) {
		j(w, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
			map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}}}})
	}
	if body.Model == "titan-guard" {
		t := strings.ToLower(last.Content)
		if strings.Contains(t, "password") || strings.Contains(t, "token") {
			say(`{"verdict":"refuse","reason":"It asks for a secret."}`)
			return
		}
		if strings.Contains(t, "install") {
			say(`{"verdict":"ask_owner","reason":"Installing things is Wizard's call."}`)
			return
		}
		say(`{"verdict":"allow","reason":"A status question or a listed fix."}`)
		return
	}
	switch last.Role {
	case "user":
		call("status", "{}")
	case "tool":
		if strings.HasPrefix(last.Content, "Offered") {
			say("It's down right now. I can fix that for you: press Yes below.")
			return
		}
		var rows []struct {
			ID, Name, Status string
			Fixes            []string `json:"fixes_allowed"`
		}
		json.Unmarshal([]byte(last.Content), &rows)
		// Prefer a service the person named.
		asked := ""
		for _, m := range msgs {
			if m.Role == "user" {
				asked = strings.ToLower(m.Content)
			}
		}
		for i, s := range rows {
			if strings.Contains(asked, strings.ToLower(strings.Fields(s.Name)[0])) {
				rows[0], rows[i] = rows[i], rows[0]
				break
			}
		}
		for _, s := range rows {
			if s.Status != "down" {
				continue
			}
			for _, f := range s.Fixes {
				if f == "start_service" || f == "restart_service" || f == "run_sync" {
					call("propose_fix", `{"service":"`+s.ID+`","action":"`+f+`"}`)
					return
				}
			}
		}
		say("Everything you can see is up.")
	default:
		say("OK.")
	}
}
