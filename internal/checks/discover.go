package checks

// Discovery: every container titan-hands can see becomes a card, without a
// line in titan.yaml. Labels on a container tune its card:
//
//	titan.enable=false        leave it out
//	titan.name=Plex           the card's name
//	titan.group=Media         its group (default "Containers")
//	titan.audience=friends    who sees it (default discover.audience, owner)
//	titan.url=http://...      the Open button (Unraid's web UI link is used otherwise)
//	titan.about=...           one line under the name
//
// Found cards are read only: fixes stay with services listed in titan.yaml
// and titan-hands' own allowlist.

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/hands"
)

func (r *Runner) discover(ctx context.Context) {
	d := r.Cfg.Discover
	if !d.Docker || r.Hands == nil || r.Hands.URL == "" {
		return
	}
	list, err := r.Hands.List(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		slog.Warn("discover", "err", err)
		r.invErr = err
		return // keep the last list's cards; their checks say what's wrong
	}
	r.invErr = nil
	r.inv = map[string]hands.Container{}
	listed := map[string]bool{}
	for _, s := range r.Cfg.Services {
		if s.Container != "" {
			listed[s.Container] = true
		}
	}
	for _, n := range d.Skip {
		listed[n] = true
	}
	var found []config.Service
	for _, c := range list {
		r.inv[c.Name] = c
		if listed[c.Name] || strings.EqualFold(c.Labels["titan.enable"], "false") {
			continue
		}
		id := "docker-" + c.Name
		if c.State != "running" && !d.Stopped {
			if _, seen := r.last[id]; !seen {
				continue // stopped before T.I.T.A.N ever saw it running: probably on purpose
			}
		}
		aud := d.Audience
		if a, ok := config.AudienceRole(c.Labels["titan.audience"]); ok && c.Labels["titan.audience"] != "" {
			aud = a
		}
		found = append(found, config.Service{
			ID: id, Name: firstNonEmpty(c.Labels["titan.name"], c.Name), Group: firstNonEmpty(c.Labels["titan.group"], "Containers"),
			About: c.Labels["titan.about"], Audience: aud, FixBy: config.Owner, Container: c.Name,
			Link:  firstNonEmpty(c.Labels["titan.url"], webUI(c.Labels["net.unraid.docker.webui"], d.Host)),
			Check: config.Check{Found: c.Name},
		})
	}
	r.found = found
}

var portTag = regexp.MustCompile(`\[PORT:(\d+)\]`)

// webUI fills in Unraid's "http://[IP]:[PORT:8096]/web" template.
func webUI(t, host string) string {
	if t == "" || (strings.Contains(t, "[IP]") && host == "") {
		return ""
	}
	t = strings.ReplaceAll(t, "[IP]", host)
	t = portTag.ReplaceAllString(t, "$1")
	if !strings.HasPrefix(t, "http://") && !strings.HasPrefix(t, "https://") {
		return ""
	}
	return t
}

// Inventory is the last container list (for the owner's page).
func (r *Runner) Inventory() ([]hands.Container, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]hands.Container, 0, len(r.inv))
	for _, c := range r.inv {
		out = append(out, c)
	}
	return out, r.invErr
}

func (r *Runner) foundState(name string) (hands.Container, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.inv[name]
	return c, ok, r.invErr
}
