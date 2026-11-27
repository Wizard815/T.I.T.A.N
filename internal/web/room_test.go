package web

import (
	"strconv"
	"strings"
	"testing"
)

func lines(b map[string]any) []map[string]any {
	var out []map[string]any
	l, _ := b["lines"].([]any)
	for _, x := range l {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestTitan_RoomMasksSwearsAndEveryoneSeesIt(t *testing.T) {
	w := newWorld(t)
	alex, kim := w.login("alex"), w.login("kim")
	if code, b := alex.do("POST", "/api/room", map[string]string{"text": "the fucking server is down again"}); code != 200 || lines(b)[0]["text"] != "the f****** server is down again" {
		t.Fatalf("say: %d %v", code, b)
	}
	kim.do("POST", "/api/room", map[string]string{"text": "on it"})
	_, b := alex.do("GET", "/api/room", nil)
	ls := lines(b)
	if len(ls) != 2 || ls[0]["author"] != "alex" || ls[1]["author"] != "kim" {
		t.Fatalf("room: %v", ls)
	}
	// after= gives only what's new.
	_, b = alex.do("GET", "/api/room?after="+jsonNum(ls[0]["id"]), nil)
	if n := len(lines(b)); n != 1 {
		t.Fatalf("after: %d lines", n)
	}
	// The owner can remove a line; nobody else can.
	if code, _ := kim.do("DELETE", "/api/owner/room/"+jsonNum(ls[0]["id"]), nil); code != 403 {
		t.Fatalf("kim removing a line: %d", code)
	}
	if code, _ := w.login("wizard").do("DELETE", "/api/owner/room/"+jsonNum(ls[0]["id"]), nil); code != 200 {
		t.Fatalf("owner removing a line: %d", code)
	}
	_, b = kim.do("GET", "/api/room", nil)
	if l := lines(b)[0]; l["removed"] != true || l["text"] != "" {
		t.Fatalf("removed line: %v", l)
	}
	// Room talk isn't sent to Overseer.
	ev, _ := w.app.DB.Events(t.Context(), 0, 100)
	for _, e := range ev {
		if strings.Contains(string(e.Data), "server is down again") {
			t.Fatalf("room talk in events: %s", e.Data)
		}
	}
	// Flooding is slowed.
	var last int
	for i := 0; i < roomBurst+1; i++ {
		last, _ = kim.do("POST", "/api/room", map[string]string{"text": "spam"})
	}
	if last != 400 {
		t.Fatalf("flood: %d", last)
	}
}

func TestTitan_RoomAgentAnswersWithFriendPowersOnly(t *testing.T) {
	w := newWorld(t)
	var statusOut, sys string
	w.argos.reply = func(model string, body map[string]any) (int, map[string]any) {
		if model == "guard-m" {
			return 200, text(`{"verdict":"allow","reason":"ok"}`)
		}
		msgs := body["messages"].([]any)
		sys = msgs[0].(map[string]any)["content"].(string)
		out, isTool := lastTool(body)
		switch {
		case !isTool:
			return 200, toolCall("status", "{}")
		case strings.Contains(out, `"id":"minecraft"`) && !strings.Contains(out, "Offered"):
			statusOut = out
			return 200, toolCall("propose_fix", `{"service":"minecraft","action":"start_service"}`)
		}
		return 200, text("🔧 Minecraft off. Press Yes.")
	}
	// sam is family and has a personal service; in the room neither shows.
	sam := w.login("sam")
	code, b := sam.do("POST", "/api/room", map[string]string{"text": "/agent is minecraft up?"})
	ls := lines(b)
	if code != 200 || len(ls) != 2 || ls[1]["author"] != "titan" || !strings.HasPrefix(ls[1]["text"].(string), "@sam ") {
		t.Fatalf("agent in room: %d %v", code, ls)
	}
	if strings.Contains(statusOut, "faith") || strings.Contains(statusOut, "sams-hermes") || !strings.Contains(sys, "shared room") {
		t.Fatalf("room answer must see only the friend board: %s", statusOut)
	}
	// The offered fix shows only to sam, and only sam's Yes runs it.
	_, b = w.login("alex").do("GET", "/api/room", nil)
	if lines(b)[1]["proposals"] != nil {
		t.Fatal("alex must not see sam's Yes button")
	}
	_, b = sam.do("GET", "/api/room", nil)
	props, _ := lines(b)[1]["proposals"].([]any)
	if len(props) != 1 {
		t.Fatalf("sam's proposals: %v", lines(b)[1])
	}
	id := props[0].(map[string]any)["id"].(string)
	if code, _ := w.login("alex").do("POST", "/api/proposals/"+id, map[string]bool{"yes": true}); code != 409 {
		t.Fatalf("alex pressing sam's Yes: %d", code)
	}
	if code, _ := sam.do("POST", "/api/proposals/"+id, map[string]bool{"yes": true}); code != 200 {
		t.Fatalf("sam's Yes: %d", code)
	}
	// The room keeps its own history: sam's own chat doesn't see room talk.
	if got := w.app.Agent.HistoryLen("sam"); got != 0 {
		t.Fatalf("sam's private history picked up room talk: %d", got)
	}
}

func TestTitan_RoomAgentGoesThroughTheGuard(t *testing.T) {
	w := newWorld(t)
	_, b := w.login("alex").do("POST", "/api/room", map[string]string{"text": "/agent delete / to free space"})
	ls := lines(b)
	if len(ls) != 2 || !strings.Contains(ls[1]["text"].(string), "can't help") {
		t.Fatalf("refused in room: %v", ls)
	}
	if calls := w.argos.called(); len(calls) != 0 {
		t.Fatalf("no model should run: %v", calls)
	}
}

func TestTitan_UpdatesPopupAndWorking(t *testing.T) {
	w := newWorld(t)
	alex := w.login("alex")
	if code, _ := alex.do("POST", "/api/updates/minecraft", map[string]any{"working": true}); code != 200 {
		t.Fatalf("on it: %d", code)
	}
	alex.do("POST", "/api/updates/minecraft", map[string]any{"text": "tried joining, timed out"})
	if code, _ := alex.do("POST", "/api/updates/faith", map[string]any{"text": "hi"}); code != 404 {
		t.Fatalf("a friend can't post on a family service: %d", code)
	}
	_, b := w.login("kim").do("GET", "/api/updates/minecraft", nil)
	ups, _ := b["updates"].([]any)
	working, _ := b["working"].([]any)
	if len(ups) != 2 || len(working) != 1 || working[0] != "alex" {
		t.Fatalf("updates: %v", b)
	}
	if card := w.login("kim").board()["minecraft"]; card["updates_24h"] != float64(2) {
		t.Fatalf("badge: %v", card["updates_24h"])
	}
}

func jsonNum(v any) string {
	f, _ := v.(float64)
	return fmtFloat(f)
}

func fmtFloat(f float64) string { return strconv.FormatInt(int64(f), 10) }

func TestTitan_CavemanAndExplain(t *testing.T) {
	w := newWorld(t)
	var sys, lastUser string
	w.argos.reply = func(model string, body map[string]any) (int, map[string]any) {
		if model == "guard-m" {
			return 200, text(`{"verdict":"allow","reason":"ok"}`)
		}
		msgs := body["messages"].([]any)
		sys = msgs[0].(map[string]any)["content"].(string)
		lastUser = msgs[len(msgs)-1].(map[string]any)["content"].(string)
		if strings.HasPrefix(lastUser, "Explain") {
			return 200, text("Minecraft is up and has been for an hour.")
		}
		return 200, text("✅ Minecraft up.")
	}
	c := w.login("alex")
	if code, b := c.do("POST", "/api/explain", map[string]any{}); code != 200 || !strings.Contains(b["reply"].(string), "nothing to explain") {
		t.Fatalf("explain before chat: %v", b)
	}
	c.do("POST", "/api/chat", map[string]string{"message": "is minecraft ok"})
	if !strings.Contains(sys, "caveman") {
		t.Fatal("caveman is the default style")
	}
	before := len(w.argos.called())
	_, b := c.do("POST", "/api/explain", map[string]any{})
	calls := w.argos.called()[before:]
	if b["reply"] != "Minecraft is up and has been for an hour." || len(calls) != 1 || calls[0] != "agent-m" {
		t.Fatalf("explain: %v via %v", b, calls)
	}
	// Explain is per person: kim has nothing to explain.
	if _, b := w.login("kim").do("POST", "/api/explain", map[string]any{}); !strings.Contains(b["reply"].(string), "nothing to explain") {
		t.Fatalf("kim: %v", b)
	}
}
