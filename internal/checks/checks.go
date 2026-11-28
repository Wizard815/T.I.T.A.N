// Package checks runs each service's check on a timer, keeps the results
// (for uptime) and notes when a service goes down or comes back.
package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
	"github.com/wizard815/titan/internal/hands"
)

// Monitor is one Uptime Kuma monitor from a published status page.
type Monitor struct {
	ID     int
	Name   string
	Group  string
	Up     bool
	Known  bool
	Ping   int64
	Detail string
}

// Uptime reads the configured uptime app (Uptime Kuma or Gatus).
func Uptime(ctx context.Context, c *http.Client, k config.Kuma) (map[string]Monitor, error) {
	if k.Kind == "gatus" {
		return Gatus(ctx, c, k.URL)
	}
	return Kuma(ctx, c, k.URL, k.Slug)
}

// Gatus reads Gatus's endpoint statuses. It answers by endpoint name.
func Gatus(ctx context.Context, c *http.Client, base string) (map[string]Monitor, error) {
	var list []struct {
		Name, Group, Key string
		Results          []struct {
			Success  bool
			Duration int64 // nanoseconds
			Errors   []string
		}
	}
	if err := getJSON(ctx, c, strings.TrimRight(base, "/")+"/api/v1/endpoints/statuses", &list); err != nil {
		return nil, fmt.Errorf("Gatus didn't answer: %v", err)
	}
	out := map[string]Monitor{}
	for i, e := range list {
		m := Monitor{ID: i + 1, Name: e.Name, Group: e.Group}
		if n := len(e.Results); n > 0 {
			last := e.Results[n-1]
			m.Known, m.Up, m.Ping = true, last.Success, last.Duration/1e6
			if len(last.Errors) > 0 {
				m.Detail = last.Errors[0]
			}
		}
		out[strings.ToLower(e.Name)] = m
	}
	return out, nil
}

// Kuma reads a published status page. It answers by monitor name.
func Kuma(ctx context.Context, c *http.Client, base, slug string) (map[string]Monitor, error) {
	base = strings.TrimRight(base, "/")
	var page struct {
		PublicGroupList []struct {
			Name        string
			MonitorList []struct {
				ID   int
				Name string
			}
		}
	}
	var hb struct {
		HeartbeatList map[string][]struct {
			Status int
			Ping   *float64
			Msg    string
		}
	}
	if err := getJSON(ctx, c, base+"/api/status-page/"+slug, &page); err != nil {
		return nil, fmt.Errorf("Uptime Kuma has no published status page %q: %v", slug, err)
	}
	if err := getJSON(ctx, c, base+"/api/status-page/heartbeat/"+slug, &hb); err != nil {
		return nil, fmt.Errorf("Uptime Kuma's heartbeats didn't load: %v", err)
	}
	out := map[string]Monitor{}
	for _, g := range page.PublicGroupList {
		for _, m := range g.MonitorList {
			mon := Monitor{ID: m.ID, Name: m.Name, Group: g.Name}
			if list := hb.HeartbeatList[strconv.Itoa(m.ID)]; len(list) > 0 {
				last := list[len(list)-1]
				mon.Known, mon.Up, mon.Detail = true, last.Status == 1, last.Msg
				if last.Ping != nil {
					mon.Ping = int64(*last.Ping)
				}
			}
			out[strings.ToLower(m.Name)] = mon
		}
	}
	return out, nil
}

func appName(kind string) string {
	if kind == "gatus" {
		return "Gatus"
	}
	return "Uptime Kuma"
}

func getJSON(ctx context.Context, c *http.Client, u string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("answered %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// Runner runs the checks.
type Runner struct {
	Cfg   *config.Config
	DB    *db.DB
	Hands *hands.Client
	HTTP  *http.Client
	Now   func() time.Time
	// OnChange is called when a service goes up or down (not on its first check).
	OnChange func(svc config.Service, r db.Result)
	// AfterRound is called after every round of checks.
	AfterRound func(ctx context.Context)

	mu      sync.Mutex
	last    map[string]db.Result
	factsBy map[string][]string
	extra   []config.Service // monitors imported from Kuma
	found   []config.Service // containers found through titan-hands
	inv     map[string]hands.Container
	invErr  error
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Services is the configured services plus any imported Kuma monitors.
func (r *Runner) Services() []config.Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]config.Service{}, r.Cfg.Services...)
	out = append(out, r.extra...)
	return append(out, r.found...)
}

// Last is the newest result for a service.
func (r *Runner) Last(id string) (db.Result, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	x, ok := r.last[id]
	return x, ok
}

// Run checks everything now and then every check_every until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.Cfg.Every.D())
	defer t.Stop()
	prune := time.Now()
	for {
		r.Round(ctx)
		if time.Since(prune) > 6*time.Hour {
			prune = time.Now()
			if err := r.DB.Prune(ctx, time.Duration(r.Cfg.KeepDays)*24*time.Hour); err != nil {
				slog.Warn("prune", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Round runs every check once.
func (r *Runner) Round(ctx context.Context) {
	var mons map[string]Monitor
	var kumaErr error
	if r.Cfg.Kuma.URL != "" {
		mons, kumaErr = Uptime(ctx, r.HTTP, r.Cfg.Kuma)
		if kumaErr != nil {
			slog.Warn("kuma", "err", kumaErr)
		}
		r.importKuma(mons)
	}
	r.discover(ctx)
	svcs := r.Services()
	results := make([]db.Result, len(svcs))
	var wg sync.WaitGroup
	for i, s := range svcs {
		wg.Add(1)
		go func(i int, s config.Service) {
			defer wg.Done()
			results[i] = r.check(ctx, s, mons, kumaErr)
			var f []string
			if len(s.Info) > 0 {
				f = r.facts(ctx, s)
			}
			r.mu.Lock()
			if r.factsBy == nil {
				r.factsBy = map[string][]string{}
			}
			r.factsBy[s.ID] = f
			r.mu.Unlock()
		}(i, s)
	}
	wg.Wait()
	// Save every result before telling anyone, so a service that went down
	// with the thing it depends on is reported as the knock-on it is.
	var changed []int
	for i, s := range svcs {
		if r.save(ctx, s, results[i]) {
			changed = append(changed, i)
		}
	}
	for _, i := range changed {
		if r.OnChange != nil {
			r.OnChange(svcs[i], results[i])
		}
	}
	if r.AfterRound != nil {
		r.AfterRound(ctx)
	}
}

// CheckOne re-runs one service's check now (the check_now action).
func (r *Runner) CheckOne(ctx context.Context, s config.Service) db.Result {
	var mons map[string]Monitor
	var err error
	if s.Check.Kuma != "" {
		mons, err = Uptime(ctx, r.HTTP, r.Cfg.Kuma)
	}
	res := r.check(ctx, s, mons, err)
	r.record(ctx, s, res)
	return res
}

func (r *Runner) importKuma(mons map[string]Monitor) {
	if !r.Cfg.Kuma.ImportAll || mons == nil {
		return
	}
	listed := map[string]bool{}
	for _, s := range r.Cfg.Services {
		if s.Check.Kuma != "" {
			listed[strings.ToLower(s.Check.Kuma)] = true
		}
	}
	var extra []config.Service
	for key, m := range mons {
		if listed[key] {
			continue
		}
		extra = append(extra, config.Service{
			ID: fmt.Sprintf("%s-%d", r.Cfg.Kuma.Kind, m.ID), Name: m.Name, Group: firstNonEmpty(m.Group, appName(r.Cfg.Kuma.Kind)),
			Audience: r.Cfg.Kuma.Audience, FixBy: config.Owner, Check: config.Check{Kuma: m.Name},
		})
	}
	r.mu.Lock()
	r.extra = extra
	r.mu.Unlock()
}

func (r *Runner) record(ctx context.Context, s config.Service, res db.Result) {
	if r.save(ctx, s, res) && r.OnChange != nil {
		r.OnChange(s, res)
	}
}

// save stores a result and reports whether the service went up or down
// (not on its first check).
func (r *Runner) save(ctx context.Context, s config.Service, res db.Result) bool {
	if err := r.DB.AddResult(ctx, s.ID, res); err != nil {
		slog.Warn("save result", "service", s.ID, "err", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]db.Result{}
	}
	prev, had := r.last[s.ID]
	r.last[s.ID] = res
	return had && prev.OK != res.OK
}

func (r *Runner) check(ctx context.Context, s config.Service, mons map[string]Monitor, kumaErr error) db.Result {
	start := r.now()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res := db.Result{At: start.Unix()}
	done := func(ok bool, detail string) db.Result {
		res.OK, res.Detail = ok, detail
		if res.MS == 0 {
			res.MS = r.now().Sub(start).Milliseconds()
		}
		return res
	}
	c := s.Check
	switch {
	case c.Found != "":
		k, ok, err := r.foundState(c.Found)
		switch {
		case err != nil:
			return done(false, "Can't see Docker right now: "+err.Error())
		case !ok:
			return done(false, "The container is gone")
		case k.State != "running":
			return done(false, "Container is "+k.State)
		case k.Health == "unhealthy":
			return done(false, "Running but unhealthy")
		}
		return done(true, "Running"+map[bool]string{true: ", healthy", false: ""}[k.Health == "healthy"])
	case c.HTTP != "":
		req, _ := http.NewRequestWithContext(ctx, "GET", c.HTTP, nil)
		resp, err := r.HTTP.Do(req)
		if err != nil {
			return done(false, "Not answering: "+shortErr(err))
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return done(false, fmt.Sprintf("Answering with an error (%d)", resp.StatusCode))
		}
		return done(true, "Answering")
	case c.Container:
		rep, err := r.Hands.Do(ctx, hands.OpState, s.Container)
		if err != nil {
			return done(false, err.Error())
		}
		st := rep.State
		if st == nil || !st.Running {
			status := "stopped"
			if st != nil {
				status = st.Status
			}
			return done(false, "Container is "+status)
		}
		if st.Health == "unhealthy" {
			return done(false, "Running but unhealthy")
		}
		d := "Running for " + Ago(r.now().Sub(st.Started))
		if st.Restarts > 0 {
			d += fmt.Sprintf(", restarted %d times", st.Restarts)
		}
		return done(true, d)
	case c.AMP:
		rep, err := r.Hands.Do(ctx, hands.OpAMPState, s.AMP)
		if err != nil {
			return done(false, err.Error())
		}
		st := rep.AMP
		if st == nil {
			return done(false, "AMP gave no status")
		}
		if !st.Running {
			return done(false, "Off (the AMP instance isn't running)")
		}
		if !st.Ready {
			return done(false, "Game is "+strings.ToLower(st.State))
		}
		d := "Ready"
		if st.MaxPlayers > 0 {
			d += fmt.Sprintf(", %d of %d players on", st.Players, st.MaxPlayers)
		}
		if st.Uptime != "" {
			d += ", up " + st.Uptime
		}
		return done(true, d)
	case c.Kuma != "":
		if kumaErr != nil {
			return done(false, kumaErr.Error())
		}
		m, ok := mons[strings.ToLower(c.Kuma)]
		if !ok {
			return done(false, fmt.Sprintf("%s has no monitor %q", appName(r.Cfg.Kuma.Kind), c.Kuma))
		}
		if !m.Known {
			return done(false, "No heartbeat yet")
		}
		res.MS = m.Ping
		if !m.Up {
			return done(false, firstNonEmpty(m.Detail, "Down in "+appName(r.Cfg.Kuma.Kind)))
		}
		return done(true, "Up in "+appName(r.Cfg.Kuma.Kind))
	case c.TCP != "":
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", c.TCP)
		if err != nil {
			return done(false, "Not answering on "+c.TCP+": "+shortErr(err))
		}
		conn.Close()
		return done(true, "Answering")
	case c.Pipeline:
		rep, err := r.Pipeline(ctx, "")
		if err != nil {
			return done(false, err.Error())
		}
		d := rep.Summary
		if len(rep.Unreadable) > 0 {
			d += ". Couldn't read: " + strings.Join(rep.Unreadable, ", ")
		}
		return done(rep.Verdict != "stuck", d)
	case c.Freshness != nil:
		var v any
		if err := getJSON(ctx, r.HTTP, c.Freshness.URL, &v); err != nil {
			return done(false, "Can't read when it last ran: "+shortErr(err))
		}
		t, ok := fieldTime(v, c.Freshness.Field)
		if !ok {
			return done(false, fmt.Sprintf("No time in %q", c.Freshness.Field))
		}
		age := r.now().Sub(t)
		d := "Last ran " + Ago(age) + " ago"
		if age > c.Freshness.MaxAge.D() {
			return done(false, d+" (should be under "+Ago(c.Freshness.MaxAge.D())+")")
		}
		return done(true, d)
	}
	return done(false, "No check set up")
}

// fieldTime reads a dot path from decoded JSON as RFC 3339 or Unix time.
func fieldTime(v any, path string) (time.Time, bool) {
	for _, p := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return time.Time{}, false
		}
		v = m[p]
	}
	switch x := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t, true
		}
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return unix(f), true
		}
	case float64:
		return unix(x), true
	}
	return time.Time{}, false
}

func unix(f float64) time.Time {
	if f > 1e12 { // milliseconds
		return time.UnixMilli(int64(f))
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9))
}

// Ago says a duration the way people do: 40s, 12 min, 3 h, 2 days.
func Ago(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i < len(s)-2 {
		s = s[i+2:]
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
