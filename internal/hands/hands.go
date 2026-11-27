// Package hands is titan-hands: the only part of T.I.T.A.N that can change
// anything. It holds the Docker socket and accepts a handful of named
// operations on containers and sync jobs from its own allowlist. There is
// no shell and no free-form command, so there is nothing to talk it into.
//
// It checks everything itself (token, allowlist, rate limit), even though
// titan checks the same things first: if titan were ever fooled, hands still
// refuses.
package hands

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Operations hands knows. Nothing else exists.
const (
	OpState   = "state"
	OpStart   = "start"
	OpRestart = "restart"
	OpLogs    = "logs"
	OpSync    = "sync"
)

var containerOps = []string{OpState, OpStart, OpRestart, OpLogs}

type Request struct {
	Op     string `json:"op"`
	Target string `json:"target"`
}

type Reply struct {
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
	State   *State    `json:"state,omitempty"`
	Lines   []string  `json:"lines,omitempty"`
	AMP     *AMPState `json:"amp,omitempty"`
}

type State struct {
	Status   string    `json:"status"` // running, exited, restarting, ...
	Running  bool      `json:"running"`
	Health   string    `json:"health,omitempty"`
	Started  time.Time `json:"started"`
	Restarts int       `json:"restarts"`
}

// Rules is hands' own allowlist.
type Rules struct {
	Containers map[string]map[string]bool // name -> allowed ops
	Syncs      map[string]string          // job -> URL to POST
	PerHour    int                        // changing ops per target per hour
	AMP        *AMPRules                  // game servers in AMP; nil = off
}

// ParseContainers reads "faith:restart:logs minecraft" (space or comma
// separated). A bare name allows state, start, restart and logs.
func ParseContainers(s string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, e := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' }) {
		parts := strings.Split(e, ":")
		name := parts[0]
		if name == "" || strings.ContainsAny(name, "/?#%") {
			return nil, fmt.Errorf("%q isn't a container name", name)
		}
		ops := map[string]bool{OpState: true}
		if len(parts) == 1 {
			for _, o := range containerOps {
				ops[o] = true
			}
		}
		for _, o := range parts[1:] {
			ok := false
			for _, k := range containerOps {
				ok = ok || k == o
			}
			if !ok {
				return nil, fmt.Errorf("%s: %q isn't an operation (use start, restart or logs)", name, o)
			}
			ops[o] = true
		}
		out[name] = ops
	}
	return out, nil
}

// ParseSyncs reads "dnd=http://host/sync wiki=http://host/pull".
func ParseSyncs(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range strings.Fields(s) {
		k, v, ok := strings.Cut(e, "=")
		u, err := url.Parse(v)
		if !ok || k == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("sync jobs look like name=http://host/path, got %q", e)
		}
		out[k] = v
	}
	return out, nil
}

// Server answers titan.
type Server struct {
	Token  string
	Rules  Rules
	Docker *http.Client // talks to the socket; see DockerClient
	HTTP   *http.Client // for sync jobs
	Now    func() time.Time

	mu   sync.Mutex
	done map[string][]time.Time
	ampc *ampClient
}

// DockerClient returns an HTTP client that dials the Docker socket.
func DockerClient(sock string) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	m.HandleFunc("POST /v1/do", s.do)
	return m
}

func write(w http.ResponseWriter, code int, r Reply) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(r)
}

func (s *Server) do(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
		write(w, 401, Reply{Message: "titan-hands needs its token (TITAN_HANDS_TOKEN, the same in both containers)."})
		return
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		write(w, 400, Reply{Message: `Send {"op": "restart", "target": "faith"}.`})
		return
	}
	rep, code := s.Do(r.Context(), req)
	write(w, code, rep)
}

// Do runs one operation after checking the allowlist and the limit.
func (s *Server) Do(ctx context.Context, req Request) (Reply, int) {
	if strings.HasPrefix(req.Op, "amp_") {
		return s.amp(ctx, req)
	}
	if req.Op == OpSync {
		u, ok := s.Rules.Syncs[req.Target]
		if !ok {
			return Reply{Message: fmt.Sprintf("%q isn't a sync job titan-hands knows.", req.Target)}, 403
		}
		if !s.take(req.Op + ":" + req.Target) {
			return Reply{Message: "That sync has run too often this hour. Ask Wizard."}, 429
		}
		return s.sync(ctx, req.Target, u)
	}
	ops, ok := s.Rules.Containers[req.Target]
	if !ok {
		return Reply{Message: fmt.Sprintf("%q isn't on titan-hands' list of containers.", req.Target)}, 403
	}
	if !ops[req.Op] {
		return Reply{Message: fmt.Sprintf("titan-hands doesn't allow %q on %s.", req.Op, req.Target)}, 403
	}
	if (req.Op == OpStart || req.Op == OpRestart) && !s.take(req.Op+":"+req.Target) {
		return Reply{Message: fmt.Sprintf("%s has been %sed too often this hour. Ask Wizard.", req.Target, req.Op)}, 429
	}
	switch req.Op {
	case OpState:
		st, err := s.state(ctx, req.Target)
		if err != nil {
			return Reply{Message: err.Error()}, 502
		}
		return Reply{OK: true, Message: st.Status, State: st}, 200
	case OpStart, OpRestart:
		path := "/containers/" + url.PathEscape(req.Target) + "/" + req.Op
		if req.Op == OpRestart {
			path += "?t=20"
		}
		code, body, err := s.docker(ctx, "POST", path)
		if err != nil {
			return Reply{Message: err.Error()}, 502
		}
		if code == 304 {
			return Reply{OK: true, Message: req.Target + " was already running."}, 200
		}
		if code/100 != 2 {
			return Reply{Message: fmt.Sprintf("Docker said %d: %s", code, dockerMsg(body))}, 502
		}
		verb := "started"
		if req.Op == OpRestart {
			verb = "restarted"
		}
		return Reply{OK: true, Message: req.Target + " " + verb + "."}, 200
	case OpLogs:
		code, body, err := s.docker(ctx, "GET", "/containers/"+url.PathEscape(req.Target)+"/logs?stdout=1&stderr=1&tail=50")
		if err != nil {
			return Reply{Message: err.Error()}, 502
		}
		if code != 200 {
			return Reply{Message: fmt.Sprintf("Docker said %d: %s", code, dockerMsg(body))}, 502
		}
		return Reply{OK: true, Message: "last 50 lines", Lines: splitLogs(body)}, 200
	}
	return Reply{Message: "unknown operation"}, 400
}

// take records a changing op if it is within the hourly limit.
func (s *Server) take(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done == nil {
		s.done = map[string][]time.Time{}
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	limit := s.Rules.PerHour
	if limit <= 0 {
		limit = 6
	}
	kept := s.done[key][:0]
	for _, t := range s.done[key] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.done[key] = kept
		return false
	}
	s.done[key] = append(kept, now)
	return true
}

func (s *Server) docker(ctx context.Context, method, path string) (int, []byte, error) {
	req, _ := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
	resp, err := s.Docker.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("titan-hands can't reach Docker: %v (is /var/run/docker.sock mounted?)", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

func (s *Server) state(ctx context.Context, name string) (*State, error) {
	code, b, err := s.docker(ctx, "GET", "/containers/"+url.PathEscape(name)+"/json")
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, fmt.Errorf("there's no container called %s", name)
	}
	if code != 200 {
		return nil, fmt.Errorf("Docker said %d: %s", code, dockerMsg(b))
	}
	var v struct {
		RestartCount int
		State        struct {
			Status    string
			Running   bool
			StartedAt time.Time
			Health    *struct{ Status string }
		}
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("Docker's answer didn't make sense: %v", err)
	}
	st := &State{Status: v.State.Status, Running: v.State.Running, Started: v.State.StartedAt, Restarts: v.RestartCount}
	if v.State.Health != nil {
		st.Health = v.State.Health.Status
	}
	return st, nil
}

func (s *Server) sync(ctx context.Context, job, u string) (Reply, int) {
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		return Reply{Message: fmt.Sprintf("The %s sync didn't answer: %v", job, err)}, 502
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return Reply{Message: fmt.Sprintf("The %s sync answered %d.", job, resp.StatusCode)}, 502
	}
	return Reply{OK: true, Message: "The " + job + " sync has started."}, 200
}

func dockerMsg(b []byte) string {
	var v struct{ Message string }
	if json.Unmarshal(b, &v) == nil && v.Message != "" {
		return v.Message
	}
	return strings.TrimSpace(string(b))
}

// splitLogs undoes Docker's stream framing (8-byte headers when the
// container has no TTY) and returns the lines.
func splitLogs(b []byte) []string {
	var text bytes.Buffer
	for len(b) >= 8 && (b[0] <= 2) && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		n := int(binary.BigEndian.Uint32(b[4:8]))
		if n > len(b)-8 {
			n = len(b) - 8
		}
		text.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	text.Write(b)
	lines := strings.Split(strings.TrimRight(text.String(), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// ---- the client titan uses ----

type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

var ErrNoHands = errors.New("titan-hands isn't set up, so T.I.T.A.N can only look, not fix (set hands.url and TITAN_HANDS_TOKEN)")

func (c *Client) Do(ctx context.Context, op, target string) (Reply, error) {
	if c == nil || c.URL == "" {
		return Reply{}, ErrNoHands
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	b, _ := json.Marshal(Request{Op: op, Target: target})
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.URL, "/")+"/v1/do", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Reply{}, fmt.Errorf("titan-hands isn't answering: %v. Check that its container is running", err)
	}
	defer resp.Body.Close()
	var rep Reply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rep); err != nil {
		return Reply{}, fmt.Errorf("titan-hands answered %d with something unexpected", resp.StatusCode)
	}
	if !rep.OK {
		return rep, errors.New(rep.Message)
	}
	return rep, nil
}
