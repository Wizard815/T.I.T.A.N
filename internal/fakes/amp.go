// Package fakes holds stand-ins for homelab services, for tests and the demo.
package fakes

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// AMP fakes the parts of AMP's API titan-hands uses: one ADS with game
// instances, logins that expire when Expire is called.
type AMP struct {
	User, Password string

	mu       sync.Mutex
	session  string
	n        int
	Calls    []string
	Running  map[string]bool // instance running
	State    map[string]int  // game state (20 = ready)
	Players  map[string]int
	ids      map[string]string
	expireAt int
}

func NewAMP(user, pw string, instances ...string) *AMP {
	a := &AMP{User: user, Password: pw, Running: map[string]bool{}, State: map[string]int{}, Players: map[string]int{}, ids: map[string]string{}}
	for i, n := range instances {
		a.ids[n] = "id-" + string(rune('a'+i))
		a.Running[n] = true
	}
	return a
}

// Expire makes the current session invalid, like AMP does after a while.
func (a *AMP) Expire() {
	a.mu.Lock()
	a.session = ""
	a.mu.Unlock()
}

func (a *AMP) Got() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string{}, a.Calls...)
}

func (a *AMP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	path := r.URL.Path
	inst := ""
	if strings.HasPrefix(path, "/API/ADSModule/Servers/") {
		rest := strings.TrimPrefix(path, "/API/ADSModule/Servers/")
		id, p, _ := strings.Cut(rest, "/")
		for n, x := range a.ids {
			if x == id {
				inst = n
			}
		}
		if inst == "" {
			w.WriteHeader(404)
			return
		}
		path = "/" + p
	}
	j := func(v any) { json.NewEncoder(w).Encode(v) }
	if strings.HasSuffix(path, "/Core/Login") {
		if body["username"] != a.User || body["password"] != a.Password {
			j(map[string]any{"success": false, "resultReason": "bad login"})
			return
		}
		a.n++
		a.session = "s" + string(rune('0'+a.n%10))
		j(map[string]any{"success": true, "sessionID": a.session})
		return
	}
	if body["SESSIONID"] != a.session || a.session == "" {
		w.WriteHeader(401)
		j(map[string]any{"Title": "Unauthorized Access"})
		return
	}
	name, _ := body["InstanceName"].(string)
	a.Calls = append(a.Calls, strings.TrimSpace(strings.TrimPrefix(path, "/API/")+" "+inst+name))
	switch {
	case path == "/API/ADSModule/GetInstances":
		var list []map[string]any
		for n, id := range a.ids {
			list = append(list, map[string]any{"InstanceID": id, "InstanceName": n, "FriendlyName": n, "Running": a.Running[n]})
		}
		j([]any{map[string]any{"AvailableInstances": list}})
	case path == "/API/ADSModule/StartInstance":
		n, _ := body["InstanceName"].(string)
		a.Running[n] = true
		a.State[n] = 20
		j(map[string]any{"Status": true})
	case path == "/API/Core/GetStatus":
		j(map[string]any{"State": a.State[inst], "Uptime": "02:14:00", "Metrics": map[string]any{"Active Users": map[string]any{"RawValue": a.Players[inst], "MaxValue": 16}}})
	case path == "/API/Core/Start", path == "/API/Core/Restart":
		a.State[inst] = 20
		j(map[string]any{"Status": true})
	default:
		w.WriteHeader(404)
	}
}
