package checks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChecks_Kuma(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status-page/home":
			json.NewEncoder(w).Encode(map[string]any{"publicGroupList": []any{map[string]any{"name": "Games", "monitorList": []any{map[string]any{"id": 3, "name": "Valheim"}, map[string]any{"id": 4, "name": "Plex"}}}}})
		case "/api/status-page/heartbeat/home":
			json.NewEncoder(w).Encode(map[string]any{"heartbeatList": map[string]any{"3": []any{map[string]any{"status": 1, "ping": 12.0}, map[string]any{"status": 0, "msg": "timeout"}}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	m, err := Kuma(context.Background(), srv.Client(), srv.URL, "home")
	if err != nil {
		t.Fatal(err)
	}
	if v := m["valheim"]; !v.Known || v.Up || v.Detail != "timeout" || v.Group != "Games" {
		t.Fatalf("valheim: %+v", v)
	}
	if p := m["plex"]; p.Known {
		t.Fatalf("plex has no beats: %+v", p)
	}
	if _, err := Kuma(context.Background(), srv.Client(), srv.URL, "nope"); err == nil {
		t.Fatal("missing page should fail")
	}
}

func TestChecks_FieldTime(t *testing.T) {
	now := time.Now()
	var v any
	json.Unmarshal([]byte(`{"sync":{"last":"`+now.Format(time.RFC3339)+`","ms":`+itoa(now.UnixMilli())+`,"s":`+itoa(now.Unix())+`}}`), &v)
	for _, f := range []string{"sync.last", "sync.ms", "sync.s"} {
		got, ok := fieldTime(v, f)
		if !ok || got.Sub(now).Abs() > time.Second {
			t.Errorf("%s: %v %v", f, got, ok)
		}
	}
	if _, ok := fieldTime(v, "sync.none"); ok {
		t.Error("missing field")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
