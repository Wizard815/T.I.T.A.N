// Package web is T.I.T.A.N's server: the board, the chat, fixes, the
// owner's pages, and the event feed Overseer reads.
package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizard815/titan/internal/agent"
	"github.com/wizard815/titan/internal/auth"
	"github.com/wizard815/titan/internal/checks"
	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
	"github.com/wizard815/titan/internal/hands"
	"github.com/wizard815/titan/internal/redact"
	"github.com/wizard815/titan/internal/skills"
)

// App holds everything and implements agent.Tools.
type App struct {
	Cfg         *config.Config
	DB          *db.DB
	Auth        *auth.Auth
	Runner      *checks.Runner
	Hands       *hands.Client
	Agent       *agent.Agent
	HTTP        *http.Client
	ExportToken string // Overseer's bearer token for /api/events
	HookToken   string // Hermes's bearer token for /api/hooks/*
	Salt        string // for pseudonyms in events
	OwnerName   string // shown to people, e.g. "Wizard"
	Secure      bool   // Secure cookies (behind HTTPS)

	Playbooks skills.Set // nil = the built-in ones

	recheck time.Duration // wait after a fix before checking again
	wg      sync.WaitGroup
	healMu  sync.Mutex
	healed  map[string]time.Time // service id -> when T.I.T.A.N last looked at it by itself
	room    room
}

var labels = map[string]string{
	config.ActCheck:   "Check again",
	config.ActStart:   "Start",
	config.ActRestart: "Restart",
	config.ActSync:    "Run sync",
	config.ActLogs:    "Recent problems",
}

func (a *App) who(name string) string { return auth.Pseudonym(a.Salt, name) }

// sees is the one visibility rule. In the room everyone reads the answer,
// so a room user (role already capped at friend) never sees personal
// services, not even their own.
func (a *App) sees(s config.Service, u agent.User) bool {
	if u.Room && s.Person != "" {
		return false
	}
	if s.Person == "" && s.Check.Found == "" && u.Runs(s.ID) {
		return true
	}
	return s.Visible(u.Role, u.Name)
}

// allows is the one fix rule. Someone handed a service to run gets every
// fix listed on it, as the owner would; the allowlist still decides what
// those are.
func (a *App) allows(s config.Service, u agent.User, act string) bool {
	if !a.sees(s, u) {
		return false
	}
	if s.Person == "" && s.Check.Found == "" && u.Runs(s.ID) {
		return s.Allows(config.Owner, u.Name, act)
	}
	return s.Allows(u.Role, u.Name, act)
}

// Runnable lists the services an owner may hand to someone to run: listed
// ones, not personal, not found containers.
func (a *App) Runnable() []config.Service {
	var out []config.Service
	for _, s := range a.Cfg.Services {
		if s.Person == "" {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) find(id string) (config.Service, bool) {
	for _, s := range a.Runner.Services() {
		if s.ID == id {
			return s, true
		}
	}
	return config.Service{}, false
}

// ---- the board ----

// Mark is an update shown on the uptime bar at the time it was posted.
type Mark struct {
	At   int64  `json:"at"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

type Card struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Group     string             `json:"group"`
	About     string             `json:"about,omitempty"`
	Personal  bool               `json:"personal,omitempty"`
	Status    string             `json:"status"` // up | down | pending
	Detail    string             `json:"detail"`
	CheckedAt int64              `json:"checked_at,omitempty"`
	MS        int64              `json:"ms,omitempty"`
	Uptime    map[string]float64 `json:"uptime"`          // "24h", "7d", "30d": 0-100, or -1
	Quiet     *db.Quiet          `json:"quiet,omitempty"` // the owner is working on it
	BeatAt    []int64            `json:"beat_at"`         // when each beat was checked
	Runs      bool               `json:"runs,omitempty"`  // the owner handed this person the service to run
	Marks     []Mark             `json:"marks"`           // updates and log lines posted in the beats' time span
	Beats     []int              `json:"beats"`           // newest last: 1 up, 0 down
	Info      []string           `json:"info"`            // extra facts ("2 people watching")
	Cause     string             `json:"cause,omitempty"` // down because this service is down
	Updates   int                `json:"updates_24h"`     // updates in the last day
	Working   []string           `json:"working"`         // who said they're on it
	Actions   []ActionView       `json:"actions"`
	Found     bool               `json:"found,omitempty"`
}

type ActionView struct {
	Action string `json:"action"`
	Label  string `json:"label"`
}

func (a *App) Board(ctx context.Context, u agent.User) ([]Card, error) {
	now := time.Now()
	counts, err := a.DB.UpdatesSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	working, err := a.DB.Working(ctx)
	if err != nil {
		return nil, err
	}
	quiets, err := a.DB.Quiets(ctx)
	if err != nil {
		return nil, err
	}
	var out []Card
	for _, s := range a.Runner.Services() {
		if !a.sees(s, u) {
			continue
		}
		c := Card{ID: s.ID, Name: s.Name, Group: s.Group, About: s.About, Personal: s.Person != "", Status: "pending", Detail: "Not checked yet", Uptime: map[string]float64{}, Beats: []int{}, Info: a.Runner.Facts(s.ID), Found: s.Check.Found != ""}
		if c.Info == nil {
			c.Info = []string{}
		}
		if q, ok := quiets[s.ID]; ok {
			c.Quiet = &q
		} else if q, ok := quiets["*"]; ok {
			c.Quiet = &q
		}
		c.Runs = u.Runs(s.ID)
		if c.Quiet != nil && u.Role != config.Owner && !c.Runs {
			c.Quiet = &db.Quiet{Service: c.Quiet.Service, Until: c.Quiet.Until} // who and why are the owner's
		}
		c.Updates, c.Working = counts[s.ID], working[s.ID]
		if c.Working == nil {
			c.Working = []string{}
		}
		recent, err := a.DB.Recent(ctx, s.ID, 40)
		if err != nil {
			return nil, err
		}
		c.Marks = []Mark{}
		for _, r := range recent {
			c.Beats = append(c.Beats, map[bool]int{true: 1, false: 0}[r.OK])
			c.BeatAt = append(c.BeatAt, r.At)
		}
		if len(recent) > 0 {
			ups, err := a.DB.Updates(ctx, s.ID, 30)
			if err != nil {
				return nil, err
			}
			for _, up := range ups {
				if up.At >= recent[0].At {
					who := up.Author
					if up.Bot {
						who = "T.I.T.A.N"
					}
					c.Marks = append(c.Marks, Mark{At: up.At, Who: who, Text: up.Text})
				}
			}
		}
		if n := len(recent); n > 0 {
			last := recent[n-1]
			c.Status, c.Detail, c.CheckedAt, c.MS = map[bool]string{true: "up", false: "down"}[last.OK], last.Detail, last.At, last.MS
			if !last.OK {
				c.Cause = a.Runner.Cause(s)
			}
		}
		for k, d := range map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour} {
			up, err := a.DB.Uptime(ctx, s.ID, now.Add(-d))
			if err != nil {
				return nil, err
			}
			if up >= 0 {
				up = float64(int(up*1000+0.5)) / 10
			}
			c.Uptime[k] = up
		}
		for _, act := range append([]string{config.ActCheck}, s.Actions...) {
			if act == config.ActLogs && u.Role != config.Owner {
				continue // family gets a summary from the helper instead
			}
			if a.allows(s, u, act) && !hasAction(c.Actions, act) {
				c.Actions = append(c.Actions, ActionView{act, labels[act]})
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func hasAction(l []ActionView, a string) bool {
	for _, x := range l {
		if x.Action == a {
			return true
		}
	}
	return false
}

// ---- agent.Tools ----

func (a *App) Status(ctx context.Context, u agent.User) (string, error) {
	cards, err := a.Board(ctx, u)
	if err != nil {
		return "", err
	}
	type row struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Status  string   `json:"status"`
		Detail  string   `json:"detail"`
		Checked string   `json:"checked,omitempty"`
		Up24h   float64  `json:"uptime_24h_percent"`
		Info    []string `json:"info,omitempty"`
		Cause   string   `json:"down_because_this_is_down,omitempty"`
		Join    string   `json:"how_to_join,omitempty"`
		Fixes   []string `json:"fixes_allowed"`
	}
	rows := []row{}
	for _, c := range cards {
		r := row{ID: c.ID, Name: c.Name, Status: c.Status, Detail: c.Detail, Up24h: c.Uptime["24h"], Info: c.Info, Cause: c.Cause, Fixes: []string{}}
		if c.CheckedAt > 0 {
			r.Checked = checks.Ago(time.Since(time.Unix(c.CheckedAt, 0))) + " ago"
		}
		s, _ := a.find(c.ID)
		r.Join = s.Join
		for _, act := range append([]string{config.ActCheck}, s.Actions...) {
			if a.allows(s, u, act) && !contains(r.Fixes, act) {
				r.Fixes = append(r.Fixes, act)
			}
		}
		rows = append(rows, r)
	}
	b, _ := json.Marshal(rows)
	return string(b), nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

var errNoService = errors.New("there's no service by that id that this person can see")

func (a *App) Problems(ctx context.Context, u agent.User, id string) (string, error) {
	s, ok := a.find(id)
	if !ok || !a.sees(s, u) {
		return "", errNoService
	}
	if !a.allows(s, u, config.ActLogs) {
		return "", fmt.Errorf("recent problems aren't available for %s", s.Name)
	}
	lines, err := a.logs(ctx, s)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func (a *App) logs(ctx context.Context, s config.Service) ([]string, error) {
	rep, err := a.Hands.Do(ctx, hands.OpLogs, s.Container)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rep.Lines))
	for _, l := range rep.Lines {
		out = append(out, redact.Line(l))
	}
	return out, nil
}

func (a *App) Propose(ctx context.Context, u agent.User, id, action string) (agent.Proposal, error) {
	s, ok := a.find(id)
	if !ok || !a.sees(s, u) {
		return agent.Proposal{}, errNoService
	}
	if action == config.ActLogs || !a.allows(s, u, action) {
		return agent.Proposal{}, fmt.Errorf("%s isn't allowed on %s for a %s", action, s.Name, u.Role)
	}
	b := make([]byte, 12)
	rand.Read(b)
	p := db.Proposal{ID: hex.EncodeToString(b), Name: u.Name, Service: s.ID, Action: action, CreatedAt: time.Now().Unix()}
	if err := a.DB.AddProposal(ctx, p); err != nil {
		return agent.Proposal{}, err
	}
	return agent.Proposal{ID: p.ID, Service: s.ID, Name: s.Name, Action: action, Label: proposalLabel(action, s.Name)}, nil
}

// proposalLabel says what pressing Yes will do: "Restart Minecraft".
func proposalLabel(action, name string) string {
	switch action {
	case config.ActStart:
		return "Start " + name
	case config.ActRestart:
		return "Restart " + name
	case config.ActSync:
		return "Run " + name + " now"
	case config.ActCheck:
		return "Check " + name + " again"
	}
	return labels[action] + " " + name
}

func (a *App) TellOwner(ctx context.Context, u agent.User, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("the message is empty")
	}
	if len(text) > 1000 {
		text = text[:1000]
	}
	return a.note(ctx, "tell_wizard", u.Name, text)
}

// Pipeline answers "is my show coming?" for people allowed to ask.
func (a *App) Pipeline(ctx context.Context, u agent.User, title string) (string, error) {
	p := a.Cfg.Pipeline
	if p == nil {
		return "", errors.New("the media pipeline isn't set up")
	}
	if !config.AtLeast(u.Role, p.Audience) {
		return "", errors.New("this person can't see the media pipeline")
	}
	rep, err := a.Runner.Pipeline(ctx, title)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(rep)
	return string(b), nil
}

// note puts something in the owner's inbox and pings ntfy if set up.
func (a *App) note(ctx context.Context, kind, name, text string) error {
	if err := a.DB.AddNote(ctx, kind, name, text); err != nil {
		return err
	}
	if u := a.Cfg.Notify.NtfyURL; u != "" {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(fmt.Sprintf("%s (%s): %s", name, kind, text)))
			req.Header.Set("Title", "T.I.T.A.N")
			if resp, err := a.HTTP.Do(req); err != nil {
				slog.Warn("ntfy", "err", err)
			} else {
				resp.Body.Close()
			}
		}()
	}
	return nil
}

// ---- running a fix ----

var (
	errGone     = errors.New("That fix isn't waiting any more. Ask again if it's still needed.")
	errNotYours = errors.New("That fix was offered to someone else.")
)

// Decide runs (yes) or drops (no) a proposal the person was offered.
func (a *App) Decide(ctx context.Context, u agent.User, id string, yes bool, via string) (string, error) {
	p, err := a.DB.GetProposal(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		return "", errGone
	}
	if err != nil {
		return "", err
	}
	if p.Name != u.Name {
		return "", errNotYours
	}
	if p.State != "pending" {
		return "", errGone
	}
	if time.Since(time.Unix(p.CreatedAt, 0)) > a.Cfg.Limits.Confirm.D() {
		a.DB.Claim(ctx, id, "expired")
		return "", errGone
	}
	if !yes {
		a.DB.Claim(ctx, id, "declined")
		a.event(ctx, "action", map[string]any{"who": a.who(u.Name), "role": u.Role, "service": p.Service, "action": p.Action, "state": "declined", "via": via})
		return "OK, I won't.", nil
	}
	s, ok := a.find(p.Service)
	// The person's role may have changed since the offer: check again.
	if !ok || !a.allows(s, u, p.Action) {
		a.DB.Claim(ctx, id, "declined")
		return "", errors.New("You can't do that any more. Ask " + a.OwnerName + ".")
	}
	if config.Changing[p.Action] && u.Role != config.Owner {
		n, err := a.DB.DoneSince(ctx, s.ID, p.Action, time.Now().Add(-time.Hour))
		if err != nil {
			return "", err
		}
		if n >= a.Cfg.Limits.PerHour {
			a.DB.Claim(ctx, id, "declined")
			a.note(ctx, "limit", u.Name, fmt.Sprintf("Wanted to %s again, but it has been done %d times this hour.", lowerFirst(proposalLabel(p.Action, s.Name)), n))
			return "", fmt.Errorf("%s has had that %d times this hour, which is the limit. I've told %s.", s.Name, n, a.OwnerName)
		}
	}
	if won, err := a.DB.Claim(ctx, id, "running"); err != nil || !won {
		return "", errGone
	}
	msg, err := a.run(ctx, s, p.Action)
	state := "done"
	if err != nil {
		state, msg = "failed", err.Error()
		a.note(ctx, "failed", u.Name, fmt.Sprintf("%s failed: %s", proposalLabel(p.Action, s.Name), msg))
	}
	a.DB.Finish(ctx, id, state, msg)
	if p.Action != config.ActCheck {
		verb := map[string]string{"done": "asked T.I.T.A.N to", "failed": "tried to"}[state]
		a.post(ctx, s.ID, fmt.Sprintf("%s %s %s: %s", u.Name, verb, lowerFirst(proposalLabel(p.Action, s.Name)), msg))
	}
	a.event(ctx, "action", map[string]any{"who": a.who(u.Name), "role": u.Role, "service": s.ID, "action": p.Action, "state": state, "result": msg, "via": via})
	if err != nil {
		return "", errors.New(msg)
	}
	return msg, nil
}

func (a *App) run(ctx context.Context, s config.Service, action string) (string, error) {
	switch action {
	case config.ActCheck:
		r := a.Runner.CheckOne(ctx, s)
		return map[bool]string{true: s.Name + " is up: ", false: s.Name + " is down: "}[r.OK] + r.Detail, nil
	case config.ActStart, config.ActRestart:
		op := map[string]string{config.ActStart: hands.OpStart, config.ActRestart: hands.OpRestart}[action]
		target := s.Container
		if s.AMP != "" { // a game in AMP: work on the instance, never the AMP container
			op = map[string]string{config.ActStart: hands.OpAMPStart, config.ActRestart: hands.OpAMPRestart}[action]
			target = s.AMP
		}
		rep, err := a.Hands.Do(ctx, op, target)
		if err != nil {
			return "", err
		}
		a.later(s)
		return rep.Message + " I'll check it again in a moment.", nil
	case config.ActSync:
		rep, err := a.Hands.Do(ctx, hands.OpSync, s.Sync)
		if err != nil {
			return "", err
		}
		a.later(s)
		return rep.Message, nil
	}
	return "", fmt.Errorf("%s isn't something T.I.T.A.N can run", action)
}

// later re-checks a service after a fix has had time to work.
func (a *App) later(s config.Service) {
	d := a.recheck
	if d == 0 {
		d = 30 * time.Second
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		time.Sleep(d)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a.Runner.CheckOne(ctx, s)
	}()
}

// respond is T.I.T.A.N looking at something that went down by itself. It
// waits one check (a blip often clears), then triage reads the check, the
// logs and the playbook and says whether a simple fix should help. If so
// and self-heal is on, it runs that fix once (once an hour at most per
// service) and the usual re-check says whether it worked; if not, the
// owner gets told why. Nothing happens while the service is quiet.
func (a *App) respond(s config.Service) {
	d := a.recheck
	if d == 0 {
		d = 30 * time.Second
	}
	time.Sleep(d)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if a.quiet(ctx, s.ID) {
		return
	}
	r := a.Runner.CheckOne(ctx, s)
	if r.OK {
		return
	}
	var fixes []string
	if a.Cfg.Heals(s) {
		for _, act := range []string{config.ActStart, config.ActRestart} {
			if contains(s.Actions, act) {
				fixes = append(fixes, act)
			}
		}
	}
	a.healMu.Lock()
	if a.healed == nil {
		a.healed = map[string]time.Time{}
	}
	t, again := a.healed[s.ID]
	again = again && time.Since(t) < time.Hour
	a.healMu.Unlock()
	if again {
		a.post(ctx, s.ID, "Still down. T.I.T.A.N already looked at it this hour, so it's left for "+a.OwnerName+".")
		a.hook(ctx, "needs_owner", s, r.Detail, "Still down after T.I.T.A.N looked at it earlier this hour.")
		return
	}
	a.healMu.Lock()
	a.healed[s.ID] = time.Now()
	a.healMu.Unlock()

	in := agent.TriageInput{Name: s.Name, Detail: r.Detail, Fixes: fixes}
	if s.Container != "" && s.Check.Found == "" {
		in.Logs, _ = a.logs(ctx, s) // titan-hands says no unless logs are allowed for it
	}
	owner := agent.User{Name: "titan", Role: config.Owner}
	for _, n := range a.SkillsAbout(owner, s.ID+" "+s.Name) {
		if body, err := a.Skill(owner, n); err == nil {
			in.Playbooks += body + "\n"
		}
	}
	var res agent.TriageResult
	var err error
	if a.Agent != nil {
		res, err = a.Agent.Triage(ctx, in)
	} else {
		err = errors.New("no helper model")
	}
	if err != nil {
		// No model to ask: the plain rule (start what's off, restart the rest).
		res = agent.TriageResult{Why: "T.I.T.A.N couldn't think it through (" + err.Error() + ")."}
		if len(fixes) > 0 {
			res.Fix = healAction(s, r.Detail)
		}
	}
	a.event(ctx, "triage", map[string]any{"service": s.ID, "detail": r.Detail, "fix": res.Fix, "why": res.Why, "model": res.Model, "logs": len(in.Logs) > 0})
	if res.Fix == "" {
		msg := "T.I.T.A.N looked: needs " + a.OwnerName + ". " + res.Why
		if !a.Cfg.Heals(s) && s.Check.Found == "" && len(s.Actions) > 0 && err == nil {
			msg += " (Self-heal is off for this one.)"
		}
		a.post(ctx, s.ID, msg)
		a.note(ctx, "needs_owner", "titan", s.Name+": "+res.Why)
		a.hook(ctx, "needs_owner", s, r.Detail, res.Why)
		return
	}
	if n, _ := a.DB.DoneSince(ctx, s.ID, res.Fix, time.Now().Add(-10*time.Minute)); n > 0 {
		return // someone just pressed Yes on it; let that play out
	}
	msg, err := a.run(ctx, s, res.Fix)
	state := "done"
	if err != nil {
		state, msg = "failed", err.Error()
	}
	a.post(ctx, s.ID, fmt.Sprintf("T.I.T.A.N looked: %s It tried to %s by itself: %s", res.Why, lowerFirst(proposalLabel(res.Fix, s.Name)), msg))
	a.event(ctx, "action", map[string]any{"who": "titan", "role": "titan", "service": s.ID, "action": res.Fix, "state": state, "result": msg, "via": "heal"})
	a.hook(ctx, "self_fix", s, r.Detail, res.Why+" Tried to "+lowerFirst(proposalLabel(res.Fix, s.Name))+": "+msg)
}

// quiet says whether the owner has asked T.I.T.A.N to leave id (or
// everything) alone for now.
func (a *App) quiet(ctx context.Context, id string) bool {
	q, err := a.DB.Quiets(ctx)
	if err != nil {
		return false
	}
	_, one := q[id]
	_, all := q["*"]
	return one || all
}

// hook tells another system (Hermes) what happened: needs_owner,
// self_fix or back_up. Names of people never go in it.
func (a *App) hook(ctx context.Context, kind string, s config.Service, detail, why string) {
	h := a.Cfg.Notify.Hook
	if h == nil || h.URL == "" {
		return
	}
	now := time.Now().Unix()
	body, _ := json.Marshal(map[string]any{"source": "titan", "event_type": kind, "kind": kind, "service": s.ID, "name": s.Name, "detail": detail, "why": why, "at": now})
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "POST", h.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if t := os.Getenv(h.TokenEnv); h.TokenEnv != "" && t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		if sec := os.Getenv(h.SecretEnv); h.SecretEnv != "" && sec != "" {
			// Hermes' generic V2 signature: HMAC-SHA256 of "<timestamp>.<body>".
			ts := strconv.FormatInt(now, 10)
			m := hmac.New(sha256.New, []byte(sec))
			m.Write([]byte(ts + "."))
			m.Write(body)
			req.Header.Set("X-Webhook-Timestamp", ts)
			req.Header.Set("X-Webhook-Signature-V2", hex.EncodeToString(m.Sum(nil)))
		}
		if resp, err := a.HTTP.Do(req); err != nil {
			slog.Warn("hook", "err", err)
		} else {
			resp.Body.Close()
		}
	}()
}

// lowerFirst turns "Restart FAITH" into "restart FAITH" for mid-sentence use.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// healAction starts what's off and restarts what's on but broken.
func healAction(s config.Service, detail string) string {
	has := func(x string) bool { return contains(s.Actions, x) }
	off := regexp.MustCompile(`(?i)\b(off|exited|stopped|not running|isn't running|created)\b`).MatchString(detail)
	if has(config.ActStart) && (off || !has(config.ActRestart)) {
		return config.ActStart
	}
	return config.ActRestart
}

// Wait lets background work finish (tests, shutdown).
func (a *App) Wait() { a.wg.Wait() }

// OnChange is the runner's hook: log the change, tell the owner when down.
func (a *App) OnChange(s config.Service, r db.Result) {
	ctx := context.Background()
	cause := ""
	if !r.OK {
		cause = a.Runner.Cause(s)
	}
	a.event(ctx, "state", map[string]any{"service": s.ID, "name": s.Name, "ok": r.OK, "detail": r.Detail, "cause": cause})
	if r.OK {
		a.post(ctx, s.ID, "Back up: "+r.Detail)
		a.DB.ClearWorking(ctx, s.ID)
	} else if cause != "" {
		a.post(ctx, s.ID, "Down because "+cause+" is down: "+r.Detail)
	} else {
		a.post(ctx, s.ID, "Went down: "+r.Detail)
	}
	if a.quiet(ctx, s.ID) {
		return // the owner is working on it: no alerts, no fixes
	}
	if r.OK {
		a.hook(ctx, "back_up", s, r.Detail, "")
	}
	// Only the root problem goes to the inbox, not every service behind it.
	if !r.OK && cause == "" {
		a.note(ctx, "down", "titan", s.Name+" is down: "+r.Detail)
		a.wg.Add(1)
		go func() { defer a.wg.Done(); a.respond(s) }()
	}
}

func (a *App) event(ctx context.Context, kind string, data any) {
	if err := a.DB.AddEvent(ctx, kind, data); err != nil {
		slog.Warn("event", "kind", kind, "err", err)
	}
}

// Chat runs one message through the guard and helper, and logs it.
func (a *App) Chat(ctx context.Context, u agent.User, text string) agent.Turn {
	t := a.Agent.Ask(ctx, u, text)
	if t.Verdict.Verdict == agent.Allow {
		t.Reply += a.joinHint(u, text, t.Reply)
	}
	who := a.who(u.Name)
	a.event(ctx, "guard", map[string]any{"who": who, "role": u.Role, "text": text, "verdict": t.Verdict.Verdict, "reason": t.Verdict.Reason, "model": t.Verdict.Model})
	switch t.Verdict.Verdict {
	case agent.Refuse:
		a.note(ctx, "refused", u.Name, fmt.Sprintf("%q (%s)", text, t.Verdict.Reason))
	case agent.AskOwner:
		a.note(ctx, "ask_owner", u.Name, fmt.Sprintf("%q (%s)", text, t.Verdict.Reason))
	default:
		a.event(ctx, "conversation", map[string]any{"who": who, "role": u.Role, "room": u.Room, "text": text, "reply": t.Reply, "model": t.Model, "calls": t.Calls, "proposals": t.Proposals})
	}
	return t
}

var aboutJoining = regexp.MustCompile(`(?i)\bjoin|let (us|me) in|get in|connect|password|server (address|ip)|\bip\b`)

// joinHint adds the owner's own "how to join" words when someone can't get
// into a game, so it doesn't depend on the model remembering to.
func (a *App) joinHint(u agent.User, text, reply string) string {
	if !aboutJoining.MatchString(text) {
		return ""
	}
	for _, id := range a.Named(u, text) {
		if s, ok := a.find(id); ok && s.Join != "" && !strings.Contains(reply, s.Join) {
			return "\n🎮 " + s.Name + ": " + s.Join
		}
	}
	return ""
}

// Explain re-says the person's last answer in full sentences.
func (a *App) Explain(ctx context.Context, u agent.User) agent.Turn {
	t := a.Agent.Explain(ctx, u)
	a.event(ctx, "conversation", map[string]any{"who": a.who(u.Name), "role": u.Role, "text": "[explain]", "reply": t.Reply, "model": t.Model})
	return t
}

// post is T.I.T.A.N's own line in a service's updates.
func (a *App) post(ctx context.Context, service, text string) {
	if err := a.DB.AddUpdate(ctx, db.Update{Service: service, Author: "titan", Bot: true, Text: text}); err != nil {
		slog.Warn("update", "err", err)
	}
}

// AddUpdate is a person's note on a service, and/or "I'm on it".
func (a *App) AddUpdate(ctx context.Context, u agent.User, id, text string, working *bool) error {
	s, ok := a.find(id)
	if !ok || !a.sees(s, u) {
		return errNoService
	}
	text = strings.TrimSpace(text)
	if len(text) > 500 {
		return errors.New("Keep updates under 500 characters.")
	}
	if working != nil {
		if err := a.DB.SetWorking(ctx, s.ID, u.Name, *working); err != nil {
			return err
		}
		if text == "" {
			text = map[bool]string{true: "is working on it", false: "stopped working on it"}[*working]
		}
	}
	if text == "" {
		return errors.New("Write something first.")
	}
	a.event(ctx, "update", map[string]any{"who": a.who(u.Name), "role": u.Role, "service": s.ID, "text": text})
	return a.DB.AddUpdate(ctx, db.Update{Service: s.ID, Author: u.Name, Text: text})
}

// Skills lists the playbooks u's helper may read. In the room that's
// friend skills only, because everyone reads the answer.
func (a *App) Skills(u agent.User) map[string]string {
	out := map[string]string{}
	for _, s := range a.playbooks().For(u.Role) {
		out[s.Name] = s.Description
	}
	return out
}

func (a *App) Skill(u agent.User, name string) (string, error) {
	s, ok := a.playbooks()[strings.TrimSpace(name)]
	if !ok || !config.AtLeast(u.Role, s.Audience) {
		return "", fmt.Errorf("there's no playbook called %q for this person", name)
	}
	return s.Body, nil
}

// SkillsAbout matches the services a message names (by id, name or
// container) to the playbooks about them that u may read.
func (a *App) SkillsAbout(u agent.User, text string) []string {
	named := map[string]bool{}
	for _, s := range a.Cfg.Services {
		if names(s, text) {
			named[s.ID] = true
		}
	}
	var out []string
	for _, k := range a.playbooks().For(u.Role) {
		for _, id := range k.Services {
			if named[id] && !contains(out, k.Name) {
				out = append(out, k.Name)
			}
		}
	}
	sort.Strings(out)
	if len(out) > 2 {
		out = out[:2]
	}
	return out
}

// Named lists the services u can see (found containers too) that text names.
func (a *App) Named(u agent.User, text string) []string {
	var out []string
	for _, s := range a.Runner.Services() {
		if a.sees(s, u) && names(s, text) {
			out = append(out, s.ID)
		}
	}
	return out
}

// names says whether text mentions s by id, name, container or AMP instance.
func names(s config.Service, text string) bool {
	text = strings.ToLower(text)
	for _, w := range []string{s.ID, s.Name, s.Container, s.AMP} {
		if w = strings.ToLower(w); len(w) >= 3 && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

var builtinSkills = sync.OnceValue(func() skills.Set { s, _ := skills.Load(""); return s })

// playbooks is the loaded set, or the built-in one when none was loaded.
func (a *App) playbooks() skills.Set {
	if a.Playbooks == nil {
		return builtinSkills()
	}
	return a.Playbooks
}

// FaithLink is FAITH's address for family and up.
func (a *App) FaithLink(u agent.User) string {
	if a.Cfg.FaithURL == "" || !config.AtLeast(u.Role, config.Family) {
		return ""
	}
	if _, err := url.Parse(a.Cfg.FaithURL); err != nil {
		return ""
	}
	return a.Cfg.FaithURL
}
