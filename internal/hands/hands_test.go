package hands

import (
	"context"
	"github.com/wizard815/titan/internal/fakes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHands_AllowlistTokenAndLimit(t *testing.T) {
	cs, err := ParseContainers("minecraft faith:restart")
	if err != nil {
		t.Fatal(err)
	}
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer docker.Close()
	now := time.Now()
	s := &Server{Token: "tok-0123456789abcdef", Rules: Rules{Containers: cs, PerHour: 2}, Docker: &http.Client{Transport: rewrite(docker.URL)}, Now: func() time.Time { return now }}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ctx := context.Background()

	if _, err := (&Client{URL: srv.URL, Token: "wrong"}).Do(ctx, OpRestart, "faith"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("bad token: %v", err)
	}
	c := &Client{URL: srv.URL, Token: "tok-0123456789abcdef"}
	for _, tc := range []struct{ op, target, want string }{
		{OpRestart, "unraid-array", "isn't on titan-hands' list"},
		{OpStart, "faith", "doesn't allow"},
		{"exec", "minecraft", "doesn't allow"},
		{OpSync, "anything", "isn't a sync job"},
		{OpRestart, "../../system/df", "isn't on"},
	} {
		if _, err := c.Do(ctx, tc.op, tc.target); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s: %v", tc.op, tc.target, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := c.Do(ctx, OpRestart, "faith"); err != nil {
			t.Fatalf("restart %d: %v", i, err)
		}
	}
	if _, err := c.Do(ctx, OpRestart, "faith"); err == nil || !strings.Contains(err.Error(), "too often") {
		t.Fatalf("limit: %v", err)
	}
	now = now.Add(61 * time.Minute)
	if _, err := c.Do(ctx, OpRestart, "faith"); err != nil {
		t.Fatalf("after an hour: %v", err)
	}
}

type rewrite string

func (u rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(string(u), "http://")
	return http.DefaultTransport.RoundTrip(r2)
}

func TestHands_ParseRules(t *testing.T) {
	if _, err := ParseContainers("faith:rm"); err == nil {
		t.Fatal("rm isn't an operation")
	}
	cs, _ := ParseContainers("a, b:logs")
	if !cs["a"][OpRestart] || cs["b"][OpRestart] || !cs["b"][OpLogs] || !cs["b"][OpState] {
		t.Fatalf("%v", cs)
	}
	if _, err := ParseSyncs("dnd=file:///etc/passwd"); err == nil {
		t.Fatal("only http(s) sync URLs")
	}
}

func TestHands_SplitLogs(t *testing.T) {
	raw := append([]byte{1, 0, 0, 0, 0, 0, 0, 4}, []byte("hi\n\n")...)
	raw = append(raw, append([]byte{2, 0, 0, 0, 0, 0, 0, 4}, []byte("oops")...)...)
	if got := splitLogs(raw); strings.Join(got, "|") != "hi||oops" {
		t.Fatalf("%q", got)
	}
	if got := splitLogs([]byte("plain tty\nline 2\n")); len(got) != 2 {
		t.Fatalf("%q", got)
	}
}

func TestHands_AMPStartRestartAndRelogin(t *testing.T) {
	amp := fakes.NewAMP("titan", "pw", "Enshrouded01", "Valheim01")
	amp.Running["Enshrouded01"] = false
	as := httptest.NewServer(amp)
	defer as.Close()
	s := &Server{Token: "tok-0123456789abcdef", Rules: Rules{PerHour: 10, AMP: &AMPRules{URL: as.URL, User: "titan", Password: "pw", Instances: ParseInstances("enshrouded01")}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c := &Client{URL: srv.URL, Token: "tok-0123456789abcdef"}
	ctx := context.Background()

	rep, err := c.Do(ctx, OpAMPState, "Enshrouded01")
	if err != nil || rep.AMP == nil || rep.AMP.Running || rep.AMP.Ready {
		t.Fatalf("stopped instance: %+v %v", rep, err)
	}
	if _, err := c.Do(ctx, OpAMPStart, "Valheim01"); err == nil || !strings.Contains(err.Error(), "isn't on titan-hands' list of AMP") {
		t.Fatalf("Valheim isn't allowed: %v", err)
	}
	if rep, err = c.Do(ctx, OpAMPStart, "Enshrouded01"); err != nil || !strings.Contains(rep.Message, "starting") {
		t.Fatalf("start: %+v %v", rep, err)
	}
	amp.Players["Enshrouded01"] = 3
	amp.Expire() // AMP logs titan-hands out; it should log in again by itself
	if rep, err = c.Do(ctx, OpAMPState, "Enshrouded01"); err != nil || !rep.AMP.Ready || rep.AMP.Players != 3 || rep.AMP.MaxPlayers != 16 {
		t.Fatalf("ready with players: %+v %v", rep.AMP, err)
	}
	if _, err = c.Do(ctx, OpAMPRestart, "enshrouded01"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	got := strings.Join(amp.Got(), "|")
	if !strings.Contains(got, "ADSModule/StartInstance Enshrouded01") || !strings.Contains(got, "Core/Restart Enshrouded01") {
		t.Fatalf("AMP calls: %s", got)
	}
	if strings.Contains(got, "Valheim01") {
		t.Fatal("Valheim was touched")
	}

	bad := &Server{Token: "tok-0123456789abcdef", Rules: Rules{AMP: &AMPRules{URL: as.URL, User: "titan", Password: "nope", Instances: ParseInstances("Enshrouded01")}}}
	if rep, _ := bad.Do(ctx, Request{Op: OpAMPState, Target: "Enshrouded01"}); !strings.Contains(rep.Message, "refused titan-hands' login") {
		t.Fatalf("bad login: %+v", rep)
	}
}
