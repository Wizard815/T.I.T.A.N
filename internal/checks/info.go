package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wizard815/titan/internal/config"
)

// Facts gathers a service's extra card lines ("2 people watching"). A fact
// that can't be read is left out and logged; it never makes a card red.
func (r *Runner) facts(ctx context.Context, s config.Service) []string {
	var out []string
	for _, in := range s.Info {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		f, err := r.fact(ctx, in)
		cancel()
		if err != nil {
			slog.Warn("card info", "service", s.ID, "err", err)
			continue
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (r *Runner) fact(ctx context.Context, in config.Info) (string, error) {
	switch {
	case in.Tautulli != nil:
		var v struct {
			Response struct {
				Data struct {
					StreamCount          any `json:"stream_count"`
					StreamCountTranscode any `json:"stream_count_transcode"`
				} `json:"data"`
			} `json:"response"`
		}
		u := strings.TrimRight(in.Tautulli.URL, "/") + "/api/v2?cmd=get_activity&apikey=" + url.QueryEscape(os.Getenv(in.Tautulli.KeyEnv))
		if err := r.getJSON(ctx, u, nil, &v); err != nil {
			return "", fmt.Errorf("tautulli: %w", err)
		}
		n, t := num(v.Response.Data.StreamCount), num(v.Response.Data.StreamCountTranscode)
		label := in.Tautulli.Label
		switch {
		case n == 0:
			return prefix(label, "Nobody watching right now"), nil
		case t > 0:
			return prefix(label, fmt.Sprintf("%s watching (%d converting, which is harder on the server)", people(n), t)), nil
		}
		return prefix(label, people(n)+" watching"), nil
	case in.Seerr != nil:
		var v struct{ Pending, Processing, Approved int }
		if err := r.getJSON(ctx, strings.TrimRight(in.Seerr.URL, "/")+"/api/v1/request/count", key("X-Api-Key", in.Seerr.KeyEnv), &v); err != nil {
			return "", fmt.Errorf("seerr: %w", err)
		}
		return prefix(in.Seerr.Label, fmt.Sprintf("%d %s waiting for approval, %d on the way", v.Pending, plural(v.Pending, "request", "requests"), v.Processing)), nil
	case in.Arr != nil:
		var v struct{ TotalRecords int }
		base := strings.TrimRight(in.Arr.URL, "/")
		err := r.getJSON(ctx, base+"/api/v3/queue?pageSize=1", key("X-Api-Key", in.Arr.KeyEnv), &v)
		if err != nil && strings.Contains(err.Error(), "404") { // Readarr and Prowlarr speak v1
			err = r.getJSON(ctx, base+"/api/v1/queue?pageSize=1", key("X-Api-Key", in.Arr.KeyEnv), &v)
		}
		if err != nil {
			return "", fmt.Errorf("arr: %w", err)
		}
		label := in.Arr.Label
		if label == "" {
			label = "Downloads"
		}
		if v.TotalRecords == 0 {
			return label + ": nothing in the queue", nil
		}
		return fmt.Sprintf("%s: %d in the queue", label, v.TotalRecords), nil
	case in.Argos != nil:
		var v struct {
			Running []struct{ Model, State string } `json:"running"`
		}
		if err := r.getJSON(ctx, strings.TrimRight(in.Argos.URL, "/")+"/running", nil, &v); err != nil {
			return "", fmt.Errorf("argos: %w", err)
		}
		var names []string
		for _, m := range v.Running {
			n := m.Model
			if m.State != "" && m.State != "ready" {
				n += " (" + m.State + ")"
			}
			names = append(names, n)
		}
		if len(names) == 0 {
			return prefix(in.Argos.Label, "No model loaded; the first question will be slow"), nil
		}
		return prefix(in.Argos.Label, "Loaded: "+strings.Join(names, ", ")), nil
	case in.JSON != nil:
		var h map[string]string
		if e := in.JSON.HeaderEnv; e != "" {
			if k, val, ok := strings.Cut(os.Getenv(e), ":"); ok {
				h = map[string]string{strings.TrimSpace(k): strings.TrimSpace(val)}
			}
		}
		var v any
		if err := r.getJSON(ctx, in.JSON.URL, h, &v); err != nil {
			return "", fmt.Errorf("%s: %w", in.JSON.Label, err)
		}
		for _, p := range strings.Split(in.JSON.Field, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				return "", fmt.Errorf("%s: no %s", in.JSON.Label, in.JSON.Field)
			}
			v = m[p]
		}
		if v == nil {
			return "", fmt.Errorf("%s: no %s", in.JSON.Label, in.JSON.Field)
		}
		s := fmt.Sprint(v)
		if f, ok := v.(float64); ok {
			s = strconv.FormatFloat(f, 'f', -1, 64)
			if f != float64(int64(f)) {
				s = strconv.FormatFloat(f, 'f', 1, 64)
			}
		}
		return in.JSON.Label + ": " + s + in.JSON.Suffix, nil
	}
	return "", nil
}

func (r *Runner) getJSON(ctx context.Context, u string, headers map[string]string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("not answering (%s)", shortErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("answered %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func key(header, env string) map[string]string {
	if env == "" {
		return nil
	}
	return map[string]string{header: os.Getenv(env)}
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func people(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func prefix(label, s string) string {
	if label == "" {
		return s
	}
	return label + ": " + s
}

// Facts is the newest extra card lines for a service.
func (r *Runner) Facts(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.factsBy[id]
}

// Cause names the service at the root of the problem when s is down because
// something it depends on is down, or "" when it isn't.
func (r *Runner) Cause(s config.Service) string {
	return r.cause(s, map[string]bool{})
}

func (r *Runner) cause(s config.Service, seen map[string]bool) string {
	if seen[s.ID] {
		return ""
	}
	seen[s.ID] = true
	for _, id := range s.DependsOn {
		d := r.Cfg.Find(id)
		if d == nil {
			continue
		}
		last, ok := r.Last(id)
		if !ok || last.OK {
			continue
		}
		if deeper := r.cause(*d, seen); deeper != "" {
			return deeper
		}
		return d.Name
	}
	return ""
}
