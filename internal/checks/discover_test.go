package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
	"github.com/wizard815/titan/internal/hands"
)

type rw string

func (u rw) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(string(u), "http://")
	return http.DefaultTransport.RoundTrip(r2)
}

func TestChecks_DiscoverContainers(t *testing.T) {
	list := `[
 {"Names":["/plex"],"Image":"plex","State":"running","Status":"Up 3 hours (healthy)","Labels":{"net.unraid.docker.webui":"http://[IP]:[PORT:32400]/web","titan.group":"Media","titan.audience":"friends"}},
 {"Names":["/immich"],"Image":"immich","State":"exited","Status":"Exited (0)"},
 {"Names":["/faith"],"Image":"faith","State":"running","Status":"Up"},
 {"Names":["/secret"],"Image":"x","State":"running","Status":"Up","Labels":{"titan.enable":"false"}},
 {"Names":["/sonarr"],"Image":"sonarr","State":"running","Status":"Up 1 hour (unhealthy)"}
]`
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(list)) }))
	defer docker.Close()
	hs := httptest.NewServer((&hands.Server{Token: "tok-0123456789abcdef", Docker: &http.Client{Transport: rw(docker.URL)}}).Handler())
	defer hs.Close()
	cfg, err := config.Parse([]byte("argos: {url: http://x}\nmodels: {guard: g, agent: a, fallback: f}\ndiscover: {docker: true, host: 192.168.20.5}\nservices:\n  - {id: faith, name: FAITH, audience: family, container: faith, check: {http: http://faith}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	d, _ := db.Open(":memory:")
	r := &Runner{Cfg: cfg, DB: d, Hands: &hands.Client{URL: hs.URL, Token: "tok-0123456789abcdef"}, HTTP: http.DefaultClient}
	r.discover(context.Background())
	by := map[string]config.Service{}
	for _, s := range r.Services() {
		by[s.ID] = s
	}
	// faith is listed already, secret opted out, immich was never seen running.
	if len(by) != 3 || by["docker-faith"].ID != "" || by["docker-secret"].ID != "" || by["docker-immich"].ID != "" {
		t.Fatalf("services: %v", by)
	}
	p := by["docker-plex"]
	if p.Group != "Media" || p.Audience != config.Friend || p.Link != "http://192.168.20.5:32400/web" || p.FixBy != config.Owner || len(p.Actions) != 0 {
		t.Fatalf("plex: %+v", p)
	}
	if by["docker-sonarr"].Audience != config.Owner {
		t.Fatal("found containers are the owner's unless labelled")
	}
	if res := r.check(context.Background(), p, nil, nil); !res.OK || res.Detail != "Running, healthy" {
		t.Fatalf("plex check: %+v", res)
	}
	if res := r.check(context.Background(), by["docker-sonarr"], nil, nil); res.OK {
		t.Fatalf("unhealthy sonarr: %+v", res)
	}
	// Once seen, a container that stops shows as down instead of vanishing.
	r.last = map[string]db.Result{"docker-sonarr": {OK: true}}
	list = strings.Replace(list, `"State":"running","Status":"Up 1 hour (unhealthy)"`, `"State":"exited","Status":"Exited (1)"`, 1)
	r.discover(context.Background())
	found := false
	for _, s := range r.Services() {
		if s.ID == "docker-sonarr" {
			found = true
			if res := r.check(context.Background(), s, nil, nil); res.OK || res.Detail != "Container is exited" {
				t.Fatalf("stopped sonarr: %+v", res)
			}
		}
	}
	if !found {
		t.Fatal("a container that was seen running and then stopped must stay on the board")
	}
}
