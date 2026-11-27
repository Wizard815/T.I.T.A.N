package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wizard815/titan/internal/config"
)

func TestChecks_CardInfo(t *testing.T) {
	t.Setenv("TEST_KEY", "k1")
	t.Setenv("TEST_HDR", "X-Token: abc")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/seerr/api/v1/request/count":
			if r.Header.Get("X-Api-Key") != "k1" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"pending":1,"processing":3,"approved":4}`))
		case "/readarr/api/v1/queue":
			w.Write([]byte(`{"totalRecords":2}`))
		case "/argos/running":
			w.Write([]byte(`{"running":[{"model":"qwen3-8b","state":"ready"},{"model":"gpt-oss-20b","state":"starting"}]}`))
		case "/unraid":
			if r.Header.Get("X-Token") != "abc" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"array":{"used_pct":81.25}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	r := &Runner{HTTP: srv.Client()}
	s := config.Service{ID: "x", Info: []config.Info{
		{Seerr: &config.Source{URL: srv.URL + "/seerr", KeyEnv: "TEST_KEY"}},
		{Arr: &config.Source{URL: srv.URL + "/readarr", Label: "Books"}},
		{Argos: &config.Source{URL: srv.URL + "/argos"}},
		{JSON: &config.JSONFact{URL: srv.URL + "/unraid", Field: "array.used_pct", Label: "Array used", Suffix: "%", HeaderEnv: "TEST_HDR"}},
		{JSON: &config.JSONFact{URL: srv.URL + "/missing", Field: "a", Label: "Gone"}},
	}}
	got := r.facts(context.Background(), s)
	want := []string{
		"1 request waiting for approval, 3 on the way",
		"Books: 2 in the queue",
		"Loaded: qwen3-8b, gpt-oss-20b (starting)",
		"Array used: 81.2%",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %q want %q", i, got[i], want[i])
		}
	}
}
