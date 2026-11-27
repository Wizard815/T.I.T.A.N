package hands

// AMP (CubeCoders) runs the game servers, Enshrouded among them, as
// instances inside one container. Restarting that container would stop
// every game, so titan-hands talks to AMP's API instead and works on one
// instance at a time:
//
//	POST /API/Core/Login                                   -> sessionID (ADS)
//	POST /API/ADSModule/GetInstances                       -> instances, InstanceID, Running
//	POST /API/ADSModule/StartInstance {InstanceName}       -> start a stopped instance
//	POST /API/ADSModule/Servers/{id}/API/Core/Login        -> sessionID (instance)
//	POST /API/ADSModule/Servers/{id}/API/Core/GetStatus    -> State, Uptime, Active Users
//	POST /API/ADSModule/Servers/{id}/API/Core/Start|Restart
//
// Only instances named in TITAN_HANDS_AMP_INSTANCES can be touched.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	OpAMPState   = "amp_state"
	OpAMPStart   = "amp_start"
	OpAMPRestart = "amp_restart"
)

// AMPRules is hands' AMP login and allowlist.
type AMPRules struct {
	URL       string
	User      string
	Password  string
	Instances map[string]bool // lowercased names
}

type AMPState struct {
	Instance   string `json:"instance"`
	Running    bool   `json:"running"` // the instance itself
	State      string `json:"state"`   // the game: Ready, Stopped, Starting, ...
	Ready      bool   `json:"ready"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"max_players"`
	Uptime     string `json:"uptime,omitempty"`
}

// AMP application states (AMP's ApplicationState enum).
var ampStates = map[int]string{
	0: "Stopped", 5: "Getting ready", 7: "Configuring", 10: "Starting", 20: "Ready", 30: "Restarting",
	40: "Stopping", 45: "Going to sleep", 50: "Sleeping", 60: "Waiting", 70: "Installing", 75: "Updating",
	80: "Waiting for input", 100: "Failed", 200: "Suspended", 250: "In maintenance", 999: "Unknown",
}

type ampClient struct {
	r    *AMPRules
	http *http.Client
	mu   sync.Mutex
	ads  string            // ADS session
	inst map[string]string // instance id -> session
}

var errAMPAuth = errors.New("AMP login expired")

func (a *ampClient) post(ctx context.Context, path string, body map[string]any) (json.RawMessage, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(a.r.URL, "/")+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("titan-hands can't reach AMP: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 || bytes.Contains(raw, []byte(`"Unauthorized Access"`)) {
		return nil, errAMPAuth
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("AMP answered %d", resp.StatusCode)
	}
	return raw, nil
}

func (a *ampClient) login(ctx context.Context, prefix string) (string, error) {
	raw, err := a.post(ctx, prefix+"/API/Core/Login", map[string]any{"username": a.r.User, "password": a.r.Password, "token": "", "rememberMe": false})
	if err != nil {
		if errors.Is(err, errAMPAuth) {
			return "", errors.New("AMP refused titan-hands' login: check TITAN_HANDS_AMP_USER and TITAN_HANDS_AMP_PASSWORD")
		}
		return "", err
	}
	var v struct {
		Success      bool   `json:"success"`
		SessionID    string `json:"sessionID"`
		ResultReason string `json:"resultReason"`
	}
	if json.Unmarshal(raw, &v) != nil || !v.Success || v.SessionID == "" {
		return "", fmt.Errorf("AMP refused titan-hands' login (%s): check TITAN_HANDS_AMP_USER and TITAN_HANDS_AMP_PASSWORD", firstNonEmpty(v.ResultReason, "no reason given"))
	}
	return v.SessionID, nil
}

// call runs an ADS (prefix "") or instance call, logging in again once when
// the session has expired.
func (a *ampClient) call(ctx context.Context, instID, method string, body map[string]any) (json.RawMessage, error) {
	prefix := ""
	if instID != "" {
		prefix = "/API/ADSModule/Servers/" + url.PathEscape(instID)
	}
	for try := 0; try < 2; try++ {
		a.mu.Lock()
		if a.inst == nil {
			a.inst = map[string]string{}
		}
		sid := a.ads
		if instID != "" {
			sid = a.inst[instID]
		}
		a.mu.Unlock()
		if sid == "" || try == 1 {
			var err error
			if sid, err = a.login(ctx, prefix); err != nil {
				return nil, err
			}
			a.mu.Lock()
			if instID == "" {
				a.ads = sid
			} else {
				a.inst[instID] = sid
			}
			a.mu.Unlock()
		}
		b := map[string]any{"SESSIONID": sid}
		for k, v := range body {
			b[k] = v
		}
		raw, err := a.post(ctx, prefix+"/API/"+method, b)
		if errors.Is(err, errAMPAuth) && try == 0 {
			continue
		}
		return raw, err
	}
	return nil, errAMPAuth
}

type ampInstance struct {
	InstanceID   string
	InstanceName string
	FriendlyName string
	Running      bool
}

func (a *ampClient) find(ctx context.Context, name string) (*ampInstance, error) {
	raw, err := a.call(ctx, "", "ADSModule/GetInstances", nil)
	if err != nil {
		return nil, err
	}
	var targets []struct{ AvailableInstances []ampInstance }
	if json.Unmarshal(raw, &targets) != nil {
		var wrapped struct {
			Result []struct{ AvailableInstances []ampInstance } `json:"result"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, errors.New("AMP's instance list didn't make sense")
		}
		targets = wrapped.Result
	}
	for _, t := range targets {
		for _, in := range t.AvailableInstances {
			if strings.EqualFold(in.InstanceName, name) || strings.EqualFold(in.FriendlyName, name) {
				return &in, nil
			}
		}
	}
	return nil, fmt.Errorf("AMP has no instance called %s", name)
}

func (a *ampClient) state(ctx context.Context, name string) (*AMPState, *ampInstance, error) {
	in, err := a.find(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	st := &AMPState{Instance: firstNonEmpty(in.FriendlyName, in.InstanceName), Running: in.Running, State: "Instance stopped"}
	if !in.Running {
		return st, in, nil
	}
	raw, err := a.call(ctx, in.InstanceID, "Core/GetStatus", nil)
	if err != nil {
		return nil, nil, err
	}
	var v struct {
		State   int
		Uptime  string
		Metrics map[string]struct{ RawValue, MaxValue float64 }
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, nil, errors.New("AMP's status didn't make sense")
	}
	st.State = firstNonEmpty(ampStates[v.State], fmt.Sprintf("State %d", v.State))
	st.Ready = v.State == 20
	st.Uptime = v.Uptime
	if u, ok := v.Metrics["Active Users"]; ok {
		st.Players, st.MaxPlayers = int(u.RawValue), int(u.MaxValue)
	}
	return st, in, nil
}

func (s *Server) amp(ctx context.Context, req Request) (Reply, int) {
	r := s.Rules.AMP
	if r == nil || r.URL == "" {
		return Reply{Message: "titan-hands has no AMP set up (TITAN_HANDS_AMP_URL)."}, 403
	}
	if !r.Instances[strings.ToLower(req.Target)] {
		return Reply{Message: fmt.Sprintf("%q isn't on titan-hands' list of AMP instances.", req.Target)}, 403
	}
	s.mu.Lock()
	if s.ampc == nil {
		c := s.HTTP
		if c == nil {
			c = &http.Client{Timeout: 60 * time.Second}
		}
		s.ampc = &ampClient{r: r, http: c}
	}
	ac := s.ampc
	s.mu.Unlock()
	if req.Op != OpAMPState && !s.take(req.Op+":"+strings.ToLower(req.Target)) {
		return Reply{Message: req.Target + " has been started or restarted too often this hour. Ask Wizard."}, 429
	}
	st, in, err := ac.state(ctx, req.Target)
	if err != nil {
		return Reply{Message: err.Error()}, 502
	}
	switch req.Op {
	case OpAMPState:
		return Reply{OK: true, Message: st.State, AMP: st}, 200
	case OpAMPStart:
		if !in.Running {
			if _, err := ac.call(ctx, "", "ADSModule/StartInstance", map[string]any{"InstanceName": in.InstanceName}); err != nil {
				return Reply{Message: err.Error()}, 502
			}
			return Reply{OK: true, Message: st.Instance + " is starting. Games can take a few minutes."}, 200
		}
		if st.Ready {
			return Reply{OK: true, Message: st.Instance + " was already running."}, 200
		}
		if _, err := ac.call(ctx, in.InstanceID, "Core/Start", nil); err != nil {
			return Reply{Message: err.Error()}, 502
		}
		return Reply{OK: true, Message: st.Instance + " is starting. Games can take a few minutes."}, 200
	case OpAMPRestart:
		if !in.Running {
			if _, err := ac.call(ctx, "", "ADSModule/StartInstance", map[string]any{"InstanceName": in.InstanceName}); err != nil {
				return Reply{Message: err.Error()}, 502
			}
			return Reply{OK: true, Message: st.Instance + " was off, so it's starting. Games can take a few minutes."}, 200
		}
		if _, err := ac.call(ctx, in.InstanceID, "Core/Restart", nil); err != nil {
			return Reply{Message: err.Error()}, 502
		}
		return Reply{OK: true, Message: st.Instance + " is restarting. Games can take a few minutes."}, 200
	}
	return Reply{Message: "unknown operation"}, 400
}

// ParseInstances reads "Enshrouded01 Valheim01" (space or comma separated).
func ParseInstances(s string) map[string]bool {
	out := map[string]bool{}
	for _, n := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' }) {
		out[strings.ToLower(n)] = true
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
