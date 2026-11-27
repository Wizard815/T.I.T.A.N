package checks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wizard815/titan/internal/config"
)

func TestChecks_Gatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"Plex","group":"Media","results":[{"success":true,"duration":12000000},{"success":false,"duration":5000000,"errors":["connection refused"]}]},{"name":"Mealie","results":[]}]`))
	}))
	defer srv.Close()
	m, err := Gatus(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if p := m["plex"]; !p.Known || p.Up || p.Detail != "connection refused" || p.Group != "Media" || p.Ping != 5 {
		t.Fatalf("plex: %+v", p)
	}
	if m["mealie"].Known {
		t.Fatal("mealie has no results")
	}
}

func TestChecks_PipelineFindsWhereItsStuck(t *testing.T) {
	paused := false
	mux := http.NewServeMux()
	mux.HandleFunc("/seerr/api/v1/request/count", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "sk" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"pending":2,"processing":1}`))
	})
	mux.HandleFunc("/sonarr/api/v3/queue", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"records": []any{
			map[string]any{"title": "x", "status": "downloading", "trackedDownloadStatus": "ok", "trackedDownloadState": "downloading", "size": 100.0, "sizeleft": 25.0, "timeleft": "00:10:00",
				"series": map[string]any{"title": "Severance"}, "episode": map[string]any{"seasonNumber": 2, "episodeNumber": 3}},
			map[string]any{"title": "y", "status": "completed", "trackedDownloadStatus": "warning", "trackedDownloadState": "importPending", "size": 100.0, "sizeleft": 0.0,
				"statusMessages": []any{map[string]any{"title": "y", "messages": []any{"No files found are eligible for import"}}},
				"series":         map[string]any{"title": "Andor"}, "episode": map[string]any{"seasonNumber": 1, "episodeNumber": 1}},
		}})
	})
	mux.HandleFunc("/sab/api", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"queue": map[string]any{"status": map[bool]string{true: "Paused", false: "Downloading"}[paused], "paused": paused, "speed": "5.0 M", "noofslots": 1, "timeleft": "0:10:00"}})
	})
	mux.HandleFunc("/taut/api/v2", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":{"data":{"recently_added":[{"title":"Dune","media_type":"movie"},{"title":"Ep","grandparent_title":"The Bear","media_type":"episode","parent_media_index":"3","media_index":"1"}]}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("T_SEERR", "sk")
	r := &Runner{HTTP: srv.Client(), Cfg: &config.Config{Pipeline: &config.Pipeline{
		Seerr:    &config.Source{URL: srv.URL + "/seerr", KeyEnv: "T_SEERR"},
		Sonarr:   &config.Source{URL: srv.URL + "/sonarr"},
		Radarr:   &config.Source{URL: srv.URL + "/nothing-here"},
		Sabnzbd:  &config.Source{URL: srv.URL + "/sab"},
		Tautulli: &config.Source{URL: srv.URL + "/taut"},
	}}}
	rep, err := r.Pipeline(context.Background(), "severance")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != "stuck" || rep.Pending != 2 || len(rep.Downloads) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	// Problems sort first, and say what happened in plain words.
	if d := rep.Downloads[0]; d.Title != "Andor S1E1" || !strings.Contains(d.Problem, "can't be moved into the library") {
		t.Fatalf("andor: %+v", d)
	}
	if !strings.Contains(rep.Match, "Severance S2E3 is downloading, 75% done, about 00:10:00 left") {
		t.Fatalf("match: %s", rep.Match)
	}
	if len(rep.Unreadable) != 1 || !strings.HasPrefix(rep.Unreadable[0], "Radarr") {
		t.Fatalf("unreadable: %v", rep.Unreadable)
	}
	if len(rep.Arrived) != 2 || rep.Arrived[1] != "The Bear S3E1" {
		t.Fatalf("arrived: %v", rep.Arrived)
	}
	rep, _ = r.Pipeline(context.Background(), "dune")
	if rep.Match != "Dune is already on Plex" {
		t.Fatalf("dune: %s", rep.Match)
	}
	paused = true
	rep, _ = r.Pipeline(context.Background(), "")
	if !strings.Contains(strings.Join(rep.Problems, "|"), "SABnzbd is paused") {
		t.Fatalf("paused: %v", rep.Problems)
	}
}
