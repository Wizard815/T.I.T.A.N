package web

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizard815/titan/internal/agent"
	"github.com/wizard815/titan/internal/auth"
	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
)

//go:embed page.html
var page []byte

const cookieName = "titan_session"

type ctxKey struct{}

func userFrom(r *http.Request) agent.User { return r.Context().Value(ctxKey{}).(agent.User) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}

// Handler is every route.
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(page)
	})
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })

	lim := &limiter{}
	m.HandleFunc("POST /api/login", a.login(lim))
	m.Handle("POST /api/logout", a.signedIn("", a.logout))
	m.Handle("GET /api/me", a.signedIn("", a.me))
	m.Handle("GET /api/board", a.signedIn("", a.board))
	m.Handle("POST /api/chat", a.signedIn("", a.chat))
	m.Handle("POST /api/explain", a.signedIn("", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.Explain(r.Context(), userFrom(r))) }))
	m.Handle("GET /api/updates/{service}", a.signedIn("", a.updates))
	m.Handle("POST /api/updates/{service}", a.signedIn("", a.postUpdate))
	m.Handle("POST /api/propose", a.signedIn("", a.propose))
	m.Handle("POST /api/proposals/{id}", a.signedIn("", a.decide))
	m.Handle("GET /api/room", a.signedIn("", a.roomGet))
	m.Handle("POST /api/room", a.signedIn("", a.roomPost))
	m.Handle("DELETE /api/owner/room/{id}", a.signedIn(config.Owner, a.roomRemove))
	m.Handle("POST /api/password", a.signedIn("", a.ownPassword))

	m.Handle("GET /api/owner/inbox", a.signedIn(config.Owner, a.inbox))
	m.Handle("POST /api/owner/inbox/{id}/done", a.signedIn(config.Owner, a.inboxDone))
	m.Handle("POST /api/owner/problems", a.signedIn(config.Owner, a.problems))
	m.Handle("GET /api/owner/users", a.signedIn(config.Owner, a.users))
	m.Handle("POST /api/owner/users", a.signedIn(config.Owner, a.addUser))
	m.Handle("POST /api/owner/users/{name}", a.signedIn(config.Owner, a.changeUser))
	m.Handle("DELETE /api/owner/users/{name}", a.signedIn(config.Owner, a.removeUser))

	m.Handle("GET /api/server", a.signedIn("", a.server))
	m.Handle("GET /api/activity", a.signedIn("", a.activity))
	m.Handle("POST /api/confirm/{service}", a.signedIn("", a.confirmUp))
	m.Handle("POST /api/owner/quiet", a.signedIn(config.Owner, a.quietPost))
	m.Handle("DELETE /api/owner/quiet/{service}", a.signedIn(config.Owner, a.quietDelete))
	m.Handle("POST /api/hooks/quiet", a.hookAuth(a.quietPost))
	m.Handle("DELETE /api/hooks/quiet/{service}", a.hookAuth(a.quietDelete))
	m.Handle("GET /api/hooks/status", a.hookAuth(a.hookStatus))

	m.HandleFunc("GET /api/events", a.events)
	return m
}

// signedIn needs a session (and at least role, if given). Writes also need
// the X-Titan header, which a form on another site can't send.
func (a *App) signedIn(role string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Header.Get("X-Titan") != "1" {
			fail(w, 403, "This request didn't come from the T.I.T.A.N page. Reload it and try again.")
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, 401, "Sign in first.")
			return
		}
		u, err := a.Auth.Session(r.Context(), c.Value)
		if err != nil {
			fail(w, 401, "Your sign-in has ended. Sign in again.")
			return
		}
		if role != "" && !config.AtLeast(u.Role, role) {
			fail(w, 403, "Only "+a.OwnerName+" can do that.")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, agent.User{Name: u.Name, Role: u.Role})))
	})
}

// limiter slows down password guessing from one address.
type limiter struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func (l *limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string][]time.Time{}
	}
	now := time.Now()
	kept := l.m[ip][:0]
	for _, t := range l.m[ip] {
		if now.Sub(t) < 10*time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 20 {
		l.m[ip] = kept
		return false
	}
	l.m[ip] = append(kept, now)
	return true
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (a *App) login(lim *limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Titan") != "1" {
			fail(w, 403, "This request didn't come from the T.I.T.A.N page. Reload it and try again.")
			return
		}
		if !lim.allow(clientIP(r)) {
			fail(w, 429, "Too many sign-in tries from here. Wait 10 minutes.")
			return
		}
		var b struct{ Name, Password string }
		if readJSON(r, &b) != nil {
			fail(w, 400, "Send your name and password.")
			return
		}
		tok, u, err := a.Auth.Login(r.Context(), b.Name, b.Password)
		if err != nil {
			fail(w, 401, err.Error())
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteStrictMode, MaxAge: int((14 * 24 * time.Hour).Seconds())})
		writeJSON(w, 200, map[string]string{"name": u.Name, "role": u.Role})
	}
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		a.Auth.Logout(r.Context(), c.Value)
	}
	a.Agent.Forget(userFrom(r).Name)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	writeJSON(w, 200, map[string]any{"name": u.Name, "role": u.Role, "owner_name": a.OwnerName, "faith_url": a.FaithLink(u), "server_view": a.ServerView(u)})
}

func (a *App) board(w http.ResponseWriter, r *http.Request) {
	cards, err := a.Board(r.Context(), userFrom(r))
	if err != nil {
		fail(w, 500, "The board didn't load: "+err.Error())
		return
	}
	if cards == nil {
		cards = []Card{}
	}
	writeJSON(w, 200, map[string]any{"services": cards, "every": int(a.Cfg.Every.D().Seconds())})
}

func (a *App) chat(w http.ResponseWriter, r *http.Request) {
	var b struct{ Message string }
	if readJSON(r, &b) != nil || strings.TrimSpace(b.Message) == "" {
		fail(w, 400, "Type a message first.")
		return
	}
	if len(b.Message) > 2000 {
		fail(w, 400, "That's a long one. Keep it under 2000 characters.")
		return
	}
	writeJSON(w, 200, a.Chat(r.Context(), userFrom(r), strings.TrimSpace(b.Message)))
}

func (a *App) propose(w http.ResponseWriter, r *http.Request) {
	var b struct{ Service, Action string }
	if readJSON(r, &b) != nil {
		fail(w, 400, `Send {"service": "minecraft", "action": "restart_service"}.`)
		return
	}
	p, err := a.Propose(r.Context(), userFrom(r), b.Service, b.Action)
	if err != nil {
		fail(w, 403, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (a *App) decide(w http.ResponseWriter, r *http.Request) {
	var b struct{ Yes bool }
	if readJSON(r, &b) != nil {
		fail(w, 400, `Send {"yes": true} or {"yes": false}.`)
		return
	}
	msg, err := a.Decide(r.Context(), userFrom(r), r.PathValue("id"), b.Yes, "titan")
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"result": msg})
}

func (a *App) updates(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	s, ok := a.find(r.PathValue("service"))
	if !ok || !a.sees(s, u) {
		fail(w, 404, "There's no service like that on your board.")
		return
	}
	list, err := a.DB.Updates(r.Context(), s.ID, 100)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	working, _ := a.DB.Working(r.Context())
	recent, _ := a.DB.Recent(r.Context(), s.ID, 1)
	writeJSON(w, 200, map[string]any{"service": s.ID, "name": s.Name, "updates": list, "working": append([]string{}, working[s.ID]...), "checked": recent})
}

func (a *App) postUpdate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text    string
		Working *bool
	}
	if readJSON(r, &b) != nil {
		fail(w, 400, `Send {"text": "..."} or {"working": true}.`)
		return
	}
	if err := a.AddUpdate(r.Context(), userFrom(r), r.PathValue("service"), b.Text, b.Working); err != nil {
		code := 400
		if errors.Is(err, errNoService) {
			code = 404
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) ownPassword(w http.ResponseWriter, r *http.Request) {
	var b struct{ Old, New string }
	if readJSON(r, &b) != nil {
		fail(w, 400, "Send your old and new password.")
		return
	}
	u := userFrom(r)
	full, err := a.DB.GetUser(r.Context(), u.Name)
	if err != nil || !authVerify(full.Hash, b.Old) {
		fail(w, 403, "Your current password isn't right.")
		return
	}
	if err := a.setPassword(r.Context(), u.Name, b.New); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"result": "Password changed. Sign in again."})
}

func (a *App) inbox(w http.ResponseWriter, r *http.Request) {
	n, err := a.DB.Notes(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if n == nil {
		n = []db.Note{}
	}
	writeJSON(w, 200, n)
}

func (a *App) inboxDone(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.DB.DoneNote(r.Context(), id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) problems(w http.ResponseWriter, r *http.Request) {
	var b struct{ Service string }
	readJSON(r, &b)
	s, ok := a.find(b.Service)
	if !ok || s.Container == "" {
		fail(w, 404, "That service has no container to read.")
		return
	}
	lines, err := a.logs(r.Context(), s)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"lines": lines})
}

func (a *App) users(w http.ResponseWriter, r *http.Request) {
	u, err := a.DB.Users(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if u == nil {
		u = []db.User{}
	}
	writeJSON(w, 200, u)
}

func (a *App) addUser(w http.ResponseWriter, r *http.Request) {
	var b struct{ Name, Role, Password string }
	if readJSON(r, &b) != nil {
		fail(w, 400, "Send a name, role and password.")
		return
	}
	name, err := a.Auth.AddUser(r.Context(), b.Name, b.Role, b.Password)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"name": name})
}

func (a *App) changeUser(w http.ResponseWriter, r *http.Request) {
	var b struct{ Role, Password string }
	if readJSON(r, &b) != nil {
		fail(w, 400, "Send a role or a password.")
		return
	}
	name := r.PathValue("name")
	if b.Role != "" {
		if !config.ValidRole(b.Role) {
			fail(w, 400, "Role must be friend, family or owner.")
			return
		}
		if name == userFrom(r).Name && b.Role != config.Owner {
			fail(w, 400, "You can't take away your own owner role. Make another owner first.")
			return
		}
		if err := a.DB.SetRole(r.Context(), name, b.Role); err != nil {
			fail(w, 404, "There's no account called "+name+".")
			return
		}
	}
	if b.Password != "" {
		if err := a.setPassword(r.Context(), name, b.Password); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) removeUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == userFrom(r).Name {
		fail(w, 400, "You can't remove your own account.")
		return
	}
	if err := a.DB.RemoveUser(r.Context(), name); err != nil {
		fail(w, 404, "There's no account called "+name+".")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// events is the feed Overseer reads: GET /api/events?after_id=N&limit=500
// with Authorization: Bearer <TITAN_EXPORT_TOKEN>.
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if a.ExportToken == "" {
		fail(w, 503, "The Overseer feed is off. Set TITAN_EXPORT_TOKEN to turn it on.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(a.ExportToken)) != 1 {
		fail(w, 401, "This feed needs TITAN_EXPORT_TOKEN as a Bearer token.")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	ev, err := a.DB.Events(r.Context(), after, limit)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	next := after
	if len(ev) > 0 {
		next = ev[len(ev)-1].ID
	}
	writeJSON(w, 200, map[string]any{"events": ev, "next_after_id": next})
}

func authVerify(hash, pw string) bool { return auth.Verify(hash, pw) }

func (a *App) setPassword(ctx context.Context, name, pw string) error {
	if err := auth.CheckPassword(pw); err != nil {
		return err
	}
	h, err := auth.Hash(pw)
	if err != nil {
		return err
	}
	if err := a.DB.SetPassword(ctx, name, h); errors.Is(err, db.ErrNotFound) {
		return errors.New("There's no account called " + name + ".")
	} else {
		return err
	}
}
