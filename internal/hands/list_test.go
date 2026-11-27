package hands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const dockerList = `[
 {"Names":["/plex"],"Image":"plexinc/pms-docker","State":"running","Status":"Up 3 hours (healthy)",
  "Ports":[{"PrivatePort":32400,"PublicPort":32400},{"PrivatePort":32400,"PublicPort":32400}],
  "Labels":{"net.unraid.docker.webui":"http://[IP]:[PORT:32400]/web","titan.group":"Media","SECRET_TOKEN":"abc","traefik.http.routers.x.rule":"Host(x)"}},
 {"Names":["/immich"],"Image":"immich","State":"exited","Status":"Exited (0) 2 days ago","Labels":{}},
 {"Names":["/sonarr"],"Image":"sonarr","State":"running","Status":"Up 1 hour (unhealthy)","Labels":null}
]`

func TestHands_ListIsReadOnlyAndKeepsOnlyKnownLabels(t *testing.T) {
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/containers/json" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(dockerList))
	}))
	defer docker.Close()
	s := &Server{Token: "tok-0123456789abcdef", Docker: &http.Client{Transport: rewrite(docker.URL)}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := &Client{URL: srv.URL, Token: "tok-0123456789abcdef"}
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[1].Name != "plex" || list[1].Health != "healthy" || len(list[1].Ports) != 1 || list[2].Health != "unhealthy" {
		t.Fatalf("list: %+v", list)
	}
	if _, bad := list[1].Labels["SECRET_TOKEN"]; bad || list[1].Labels["titan.group"] != "Media" || len(list[1].Labels) != 2 {
		t.Fatalf("labels must be filtered: %v", list[1].Labels)
	}
	s.Rules.NoDiscover = true
	if _, err := c.List(context.Background()); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("off: %v", err)
	}
}
