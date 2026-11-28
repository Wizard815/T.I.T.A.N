package web

// The room: one IRC-style chat for everyone signed in. People talk to each
// other; swearing and slurs are masked before a line is saved. A line that
// starts with /titan (or the old /agent) goes to T.I.T.A.N, which answers in the room.
//
// Everyone in the room reads the answer, so /titan runs with friend powers
// and no personal services, whatever the asker's role, and with the room's
// own history (never the asker's private chat). Fixes it offers still wait
// for the asker's own Yes. Room talk is not sent to Overseer; /titan asks
// are, like any chat, under a pseudonym.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wizard815/titan/internal/agent"
	"github.com/wizard815/titan/internal/censor"
	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
	"net/http"
)

const (
	roomMax    = 500 // characters in a line
	roomBurst  = 8   // lines a person may send in roomWindow
	roomWindow = 30 * time.Second
)

type room struct {
	once   sync.Once
	filter *censor.Filter
	mu     sync.Mutex
	sent   map[string][]time.Time
}

func (a *App) censor() *censor.Filter {
	a.room.once.Do(func() { a.room.filter = censor.New(a.Cfg.Room.Words) })
	return a.room.filter
}

// slow reports whether name has sent too many lines just now.
func (a *App) slow(name string) bool {
	r := &a.room
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = map[string][]time.Time{}
	}
	now := time.Now()
	keep := r.sent[name][:0]
	for _, t := range r.sent[name] {
		if now.Sub(t) < roomWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) >= roomBurst {
		r.sent[name] = keep
		return true
	}
	r.sent[name] = append(keep, now)
	return false
}

// RoomUser is how /titan sees the asker: friend powers at most.
func RoomUser(u agent.User) agent.User {
	ru := agent.User{Name: u.Name, Role: u.Role, Room: true}
	if config.AtLeast(ru.Role, config.Family) {
		ru.Role = config.Friend
	}
	return ru
}

var errRoomOff = errors.New("The room chat is turned off.")

// Say adds a person's line to the room, and answers it when it's /titan.
func (a *App) Say(ctx context.Context, u agent.User, text string) ([]db.Line, error) {
	if a.Cfg.Room.Off {
		return nil, errRoomOff
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("Type something first.")
	}
	if len([]rune(text)) > roomMax {
		return nil, errors.New("Keep it under 500 characters.")
	}
	if a.slow(u.Name) {
		return nil, errors.New("Slow down a little, then try again.")
	}
	clean, _ := a.censor().Clean(text)
	id, err := a.DB.AddLine(ctx, db.Line{Author: u.Name, Text: clean})
	if err != nil {
		return nil, err
	}
	out := []db.Line{{ID: id, At: time.Now().Unix(), Author: u.Name, Text: clean}}
	ask, isAgent := agentAsk(text)
	if !isAgent {
		return out, nil
	}
	if ask == "" {
		ask = "hi"
	}
	t := a.Chat(ctx, RoomUser(u), ask)
	reply, _ := a.censor().Clean(t.Reply)
	l := db.Line{Author: "titan", Bot: true, Text: "@" + u.Name + " " + reply}
	if len(t.Proposals) > 0 {
		l.For = u.Name
		l.Proposals, _ = json.Marshal(t.Proposals)
	}
	bid, err := a.DB.AddLine(ctx, l)
	if err != nil {
		return out, err
	}
	l.ID, l.At = bid, time.Now().Unix()
	return append(out, l), nil
}

// agentAsk spots "/titan message" (and the old "/agent message").
func agentAsk(text string) (string, bool) {
	for _, c := range []string{"/titan", "/agent"} {
		if rest, ok := strings.CutPrefix(text, c); ok && (rest == "" || rest[0] == ' ') {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// Lines is the room as u sees it: offered fixes show only to whoever they
// wait for.
func (a *App) Lines(ctx context.Context, u agent.User, after int64) ([]db.Line, error) {
	if a.Cfg.Room.Off {
		return nil, errRoomOff
	}
	ls, err := a.DB.Lines(ctx, after, 200)
	if err != nil {
		return nil, err
	}
	for i := range ls {
		if ls[i].For != u.Name {
			ls[i].Proposals = nil
		}
	}
	return ls, nil
}

func (a *App) roomGet(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	ls, err := a.Lines(r.Context(), userFrom(r), after)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"lines": ls})
}

func (a *App) roomPost(w http.ResponseWriter, r *http.Request) {
	var b struct{ Text string }
	if readJSON(r, &b) != nil {
		fail(w, 400, `Send {"text": "..."}.`)
		return
	}
	ls, err := a.Say(r.Context(), userFrom(r), b.Text)
	if err != nil && ls == nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"lines": ls})
}

func (a *App) roomRemove(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.DB.RemoveLine(r.Context(), id); err != nil {
		fail(w, 404, "There's no line like that.")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
