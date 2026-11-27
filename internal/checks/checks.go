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

	mu    sync.Mutex
	last  map[string]db.Result
	extra []config.Service // monitors imported from Kuma
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
	return append(out, r.extra...)
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
		mons, kumaErr = Kuma(ctx, r.HTTP, r.Cfg.Kuma.URL, r.Cfg.Kuma.Slug)
		if kumaErr != nil {
			slog.Warn("kuma", "err", kumaErr)
		}
		r.importKuma(mons)
	}
	var wg sync.WaitGroup
	for _, s := range r.Services() {
		wg.Add(1)
		go func(s config.Service) {
			defer wg.Done()
			r.record(ctx, s, r.check(ctx, s, mons, kumaErr))
		}(s)
	}
	wg.Wait()
}

// CheckOne re-runs one service's check now (the check_now action).
func (r *Runner) CheckOne(ctx context.Context, s config.Service) db.Result {
	var mons map[string]Monitor
	var err error
	if s.Check.Kuma != "" {
		mons, err = Kuma(ctx, r.HTTP, r.Cfg.Kuma.URL, r.Cfg.Kuma.Slug)
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
			ID: fmt.Sprintf("kuma-%d", m.ID), Name: m.Name, Group: firstNonEmpty(m.Group, "Uptime Kuma"),
			Audience: r.Cfg.Kuma.Audience, FixBy: config.Owner, Check: config.Check{Kuma: m.Name},
		})
	}
	r.mu.Lock()
	r.extra = extra
	r.mu.Unlock()
}

func (r *Runner) record(ctx context.Context, s config.Service, res db.Result) {
	if err := r.DB.AddResult(ctx, s.ID, res); err != nil {
		slog.Warn("save result", "service", s.ID, "err", err)
	}
	r.mu.Lock()
	if r.last == nil {
		r.last = map[string]db.Result{}
	}
	prev, had := r.last[s.ID]
	r.last[s.ID] = res
	r.mu.Unlock()
	if had && prev.OK != res.OK && r.OnChange != nil {
		r.OnChange(s, res)
	}
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
	case c.Kuma != "":
		if kumaErr != nil {
			return done(false, "Uptime Kuma: "+kumaErr.Error())
		}
		m, ok := mons[strings.ToLower(c.Kuma)]
		if !ok {
			return done(false, fmt.Sprintf("Uptime Kuma's status page has no monitor %q", c.Kuma))
		}
		if !m.Known {
			return done(false, "No heartbeat yet")
		}
		res.MS = m.Ping
		if !m.Up {
			return done(false, firstNonEmpty(m.Detail, "Down in Uptime Kuma"))
		}
		return done(true, "Up in Uptime Kuma")
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
