package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDecisions answers with fixed yes-probabilities and counts calls.
func fakeDecisions(t *testing.T, p map[string]float64, calls *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		var in struct {
			Model     string
			State     string
			Questions map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Model == "" || !strings.Contains(in.State, "message") {
			t.Errorf("bad ranker request: %v %+v", err, in)
		}
		ans := map[string]any{}
		for k, q := range in.Questions {
			if q.(map[string]any)["type"] == "noul" {
				ans[k] = map[string]any{"type": "noul", "noul": p[k]}
				continue
			}
			ans[k] = map[string]any{"probabilities": map[string]float64{"yes": p[k], "no": 1 - p[k]}}
		}
		json.NewEncoder(w).Encode(map[string]any{"answers": ans})
	}))
}

func TestTitan_RankerDecidesClearCases(t *testing.T) {
	cases := []struct {
		name string
		p    map[string]float64
		want string
	}{
		{"fix", map[string]float64{"harm": .05, "secret": .02, "override": .01, "setup": .1}, Allow},
		{"wipe", map[string]float64{"harm": .9, "secret": .02, "override": .1, "setup": .2}, Refuse},
		{"password", map[string]float64{"harm": .1, "secret": .8, "override": .1, "setup": .3}, Refuse},
		{"install", map[string]float64{"harm": .1, "secret": .1, "override": .05, "setup": .9}, AskOwner},
	}
	for _, c := range cases {
		calls := 0
		srv := fakeDecisions(t, c.p, &calls)
		for _, model := range []string{"baseten/joey-1", "respan/span-01-lite"} {
			calls = 0
			a := &Agent{Ranker: &Ranker{URL: srv.URL, Model: model}, Owner: "Wizard"}
			v := a.Check(context.Background(), User{Name: "x", Role: "family"}, "some message")
			if v.Verdict != c.want || v.Model != model || calls != 1 {
				t.Errorf("%s with %s: got %+v after %d calls, want %s", c.name, model, v, calls, c.want)
			}
		}
		srv.Close()
	}
}

func TestTitan_RankerUnsureOrDownFailsClosed(t *testing.T) {
	calls := 0
	srv := fakeDecisions(t, map[string]float64{"harm": .5, "secret": .1, "override": .1, "setup": .1}, &calls)
	defer srv.Close()
	a := &Agent{Ranker: &Ranker{URL: srv.URL, Model: "baseten/joey-1"}}
	if v := a.Check(context.Background(), User{Role: "friend"}, "hmm"); v.Verdict != AskOwner {
		t.Errorf("unsure with no guard: got %+v", v)
	}
	a.Ranker.URL = "http://127.0.0.1:1/none"
	if v := a.Check(context.Background(), User{Role: "friend"}, "hmm"); v.Verdict != AskOwner {
		t.Errorf("ranker down with no guard: got %+v", v)
	}
	// The regex screen still runs before the ranker.
	calls = 0
	a.Ranker.URL = srv.URL
	if v := a.Check(context.Background(), User{Role: "owner"}, "rm -rf /mnt/user"); v.Verdict != Refuse || calls != 0 {
		t.Errorf("screen skipped: %+v, %d ranker calls", v, calls)
	}
}
