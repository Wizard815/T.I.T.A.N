// Package web is T.I.T.A.N's server: the board, the chat, fixes, the
// owner's pages, and the event feed Overseer reads.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
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
	GlanceToken string // DASH's bearer token for /api/glance (read-only)
	Salt        string // for pseudonyms in events
	OwnerName   string // shown to people, e.g. "Wizard"
	Secure      bool   // Secure cookies (behind HTTPS)

	recheck time.Duration // wait after a fix before checking again
	wg      sync.WaitGroup
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
	return s.Visible(u.Role, u.Name)
}

func (a *App) allows(s config.Service, u agent.User, act string) bool {
	return a.sees(s, u) && s.Allows(u.Role, u.Name, act)
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
	Beats     []int              `json:"beats"`           // newest last: 1 up, 0 down
	Info      []string           `json:"info"`            // extra facts ("2 people watching")
	Cause     string             `json:"cause,omitempty"` // down because this service is down
	Updates   int                `json:"updates_24h"`     // updates in the last day
	Working   []string           `json:"working"`         // who said they're on it
	Actions   []ActionView       `json:"actions"`
	Link      string             `json:"link,omitempty"` // the Open button
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
	var out []Card
	for _, s := range a.Runner.Services() {
		if !a.sees(s, u) {
			continue
		}
		c := Card{ID: s.ID, Name: s.Name, Group: s.Group, About: s.About, Personal: s.Person != "", Status: "pending", Detail: "Not checked yet", Uptime: map[string]float64{}, Beats: []int{}, Info: a.Runner.Facts(s.ID), Link: s.Link, Found: s.Check.Found != ""}
		if c.Info == nil {
			c.Info = []string{}
		}
		c.Updates, c.Working = counts[s.ID], working[s.ID]
		if c.Working == nil {
			c.Working = []string{}
		}
		recent, err := a.DB.Recent(ctx, s.ID, 40)
		if err != nil {
			return nil, err
		}
		for _, r := range recent {
			c.Beats = append(c.Beats, map[bool]int{true: 1, false: 0}[r.OK])
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
		Fixes   []string `json:"fixes_allowed"`
	}
	rows := []row{}
	for _, c := range cards {
		r := row{ID: c.ID, Name: c.Name, Status: c.Status, Detail: c.Detail, Up24h: c.Uptime["24h"], Info: c.Info, Cause: c.Cause, Fixes: []string{}}
		if c.CheckedAt > 0 {
			r.Checked = checks.Ago(time.Since(time.Unix(c.CheckedAt, 0))) + " ago"
		}
		s, _ := a.find(c.ID)
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
			a.note(ctx, "limit", u.Name, fmt.Sprintf("Wanted to %s again, but it has been done %d times this hour.", strings.ToLower(proposalLabel(p.Action, s.Name)), n))
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
		a.post(ctx, s.ID, fmt.Sprintf("%s %s %s: %s", u.Name, verb, strings.ToLower(proposalLabel(p.Action, s.Name)), msg))
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
	// Only the root problem goes to the inbox, not every service behind it.
	if !r.OK && cause == "" {
		a.note(ctx, "down", "titan", s.Name+" is down: "+r.Detail)
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
