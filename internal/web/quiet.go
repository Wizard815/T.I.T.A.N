package web

// Quiet: the owner is working on something ("messing with plex"), so
// T.I.T.A.N shouldn't alert, look into it or fix it by itself. Set from the
// board, from chat (the owner's quiet tool), or from Hermes with
// TITAN_HOOK_TOKEN.

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wizard815/titan/internal/agent"
	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
)

// SetQuiet makes a service ("*" or "all" = everything) quiet for hours
// (0 = until lifted), or lifts it when hours < 0. Owner only.
func (a *App) SetQuiet(ctx context.Context, u agent.User, id string, hours float64, why string) (string, error) {
	if u.Role != config.Owner || u.Room {
		return "", errors.New("only the owner can make things quiet")
	}
	id = strings.TrimSpace(id)
	name := "Everything"
	if id == "all" || id == "" {
		id = "*"
	}
	if id != "*" {
		s, ok := a.find(id)
		if !ok {
			return "", errNoService
		}
		id, name = s.ID, s.Name
	}
	if hours < 0 {
		if err := a.DB.LiftQuiet(ctx, id); err != nil {
			return "", err
		}
		a.event(ctx, "quiet", map[string]any{"service": id, "on": false})
		return name + " isn't quiet any more.", nil
	}
	if hours > 24*7 {
		hours = 24 * 7
	}
	q := db.Quiet{Service: id, By: u.Name, Why: strings.TrimSpace(why)}
	when := "until you lift it"
	if hours > 0 {
		t := time.Now().Add(time.Duration(hours * float64(time.Hour)))
		q.Until = t.Unix()
		when = "for " + strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", hours), "0"), ".") + " h"
	}
	if len(q.Why) > 200 {
		q.Why = q.Why[:200]
	}
	if err := a.DB.SetQuiet(ctx, q); err != nil {
		return "", err
	}
	a.event(ctx, "quiet", map[string]any{"service": id, "on": true, "until": q.Until})
	return fmt.Sprintf("%s is quiet %s: no alerts, no automatic checks or fixes.", name, when), nil
}

func (a *App) quietPost(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Service string  `json:"service"`
		Hours   float64 `json:"hours"`
		Why     string  `json:"why"`
	}
	if err := readJSON(r, &b); err != nil {
		fail(w, 400, "That request didn't make sense.")
		return
	}
	msg, err := a.SetQuiet(r.Context(), caller(r), b.Service, b.Hours, b.Why)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"result": msg})
}

func (a *App) quietDelete(w http.ResponseWriter, r *http.Request) {
	msg, err := a.SetQuiet(r.Context(), caller(r), r.PathValue("service"), -1, "")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"result": msg})
}

// caller is the signed-in person, or Hermes (as the owner) on a hook route.
func caller(r *http.Request) agent.User {
	if u, ok := r.Context().Value(ctxKey{}).(agent.User); ok {
		return u
	}
	return agent.User{Name: "hermes", Role: config.Owner}
}

// hookAuth lets Hermes in with TITAN_HOOK_TOKEN, as the owner, for the
// few routes under /api/hooks.
func (a *App) hookAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.HookToken == "" {
			fail(w, 503, "Hooks are off. Set TITAN_HOOK_TOKEN to turn them on.")
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.HookToken)) != 1 {
			fail(w, 401, "This needs TITAN_HOOK_TOKEN as a Bearer token.")
			return
		}
		h(w, r)
	})
}

// hookStatus is the owner's status list, for Hermes.
func (a *App) hookStatus(w http.ResponseWriter, r *http.Request) {
	s, err := a.Status(r.Context(), agent.User{Name: "hermes", Role: config.Owner})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(s))
}

// ServerView says whether u sees the server panel.
func (a *App) ServerView(u agent.User) bool {
	v := a.Cfg.ServerView
	if v == "" {
		v = config.Owner
	}
	if v == "off" || a.Hands == nil || u.Room {
		return false
	}
	need, ok := config.AudienceRole(v)
	return ok && config.AtLeast(u.Role, need)
}

var serverCache struct {
	sync.Mutex
	at  time.Time
	out any
}

// server is the panel's data, at most every 3 seconds from titan-hands.
func (a *App) server(w http.ResponseWriter, r *http.Request) {
	if !a.ServerView(userFrom(r)) {
		fail(w, 403, "The server view is the owner's.")
		return
	}
	serverCache.Lock()
	defer serverCache.Unlock()
	if time.Since(serverCache.at) > 3*time.Second {
		h, err := a.Hands.Host(r.Context())
		if err != nil {
			fail(w, 502, "Can't see the server: "+err.Error())
			return
		}
		serverCache.at, serverCache.out = time.Now(), h
	}
	writeJSON(w, 200, map[string]any{"server": serverCache.out})
}
