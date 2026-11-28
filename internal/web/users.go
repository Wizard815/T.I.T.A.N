package web

// Accounts, for the owner: add, rename, reset to "changeme", remove, and
// hand someone a service to run. After a reset the person signs in with
// changeme and must pick a new password (and name, if asked) before
// anything else works.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wizard815/titan/internal/auth"
	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
)

func (a *App) users(w http.ResponseWriter, r *http.Request) {
	us, err := a.DB.Users(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if us == nil {
		us = []db.User{}
	}
	for i := range us {
		us[i].Manages, _ = a.DB.Grants(r.Context(), us[i].Name)
	}
	runnable := []map[string]string{}
	for _, s := range a.Runnable() {
		runnable = append(runnable, map[string]string{"id": s.ID, "name": s.Name})
	}
	writeJSON(w, 200, map[string]any{"users": us, "runnable": runnable})
}

// addUser makes an account. With no password it gets changeme, and the
// person picks their own at the first sign-in.
func (a *App) addUser(w http.ResponseWriter, r *http.Request) {
	var b struct{ Name, Role, Password string }
	if readJSON(r, &b) != nil {
		fail(w, 400, "Send a name and a role.")
		return
	}
	temp := strings.TrimSpace(b.Password) == ""
	pw := b.Password
	if temp {
		pw = "placeholder-" + auth.NewToken() // replaced by the reset below
	}
	name, err := a.Auth.AddUser(r.Context(), b.Name, b.Role, pw)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if temp {
		if err := a.reset(r.Context(), name, db.ChangePassword); err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"name": name, "temp": temp})
}

func (a *App) reset(ctx context.Context, name string, mode int) error {
	h, err := auth.Hash(auth.TempPassword)
	if err != nil {
		return err
	}
	if err := a.DB.Reset(ctx, name, h, mode, time.Now().Add(auth.ResetFor)); errors.Is(err, db.ErrNotFound) {
		return errors.New("There's no account called " + name + ".")
	} else {
		return err
	}
}

// changeUser is the owner's edit: role, password, rename, reset, and the
// services this person runs. Each field is optional.
func (a *App) changeUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Role     string
		Password string
		Rename   string
		Reset    string    // "password" or "name"
		Runs     *[]string // services this person runs; [] takes them all away
	}
	if readJSON(r, &b) != nil {
		fail(w, 400, "That request didn't make sense.")
		return
	}
	ctx, me := r.Context(), userFrom(r)
	name := r.PathValue("name")
	if _, err := a.DB.GetUser(ctx, name); err != nil {
		fail(w, 404, "There's no account called "+name+".")
		return
	}
	if b.Role != "" {
		if !config.ValidRole(b.Role) {
			fail(w, 400, "Role must be friend, family or owner.")
			return
		}
		if name == me.Name && b.Role != config.Owner {
			fail(w, 400, "You can't take away your own owner role. Make another owner first.")
			return
		}
		if err := a.DB.SetRole(ctx, name, b.Role); err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	if b.Runs != nil {
		ok := map[string]bool{}
		for _, s := range a.Runnable() {
			ok[s.ID] = true
		}
		for _, id := range *b.Runs {
			if !ok[id] {
				fail(w, 400, "Only listed services can be handed to someone, not "+id+". (Personal services and found containers can't.)")
				return
			}
		}
		if err := a.DB.SetGrants(ctx, name, *b.Runs); err != nil {
			fail(w, 500, err.Error())
			return
		}
		a.event(ctx, "grants", map[string]any{"who": a.who(name), "runs": *b.Runs})
	}
	if b.Password != "" {
		if err := a.setPassword(ctx, name, b.Password); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if b.Reset != "" {
		if name == me.Name {
			fail(w, 400, "Change your own password under Account instead.")
			return
		}
		mode := map[string]int{"password": db.ChangePassword, "name": db.ChangeName}[b.Reset]
		if mode == 0 {
			fail(w, 400, `Reset is "password" or "name".`)
			return
		}
		if err := a.reset(ctx, name, mode); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	out := name
	if b.Rename != "" {
		nn, err := auth.CheckName(b.Rename)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if name == me.Name {
			fail(w, 400, "Rename yourself from another owner account.")
			return
		}
		if err := a.DB.Rename(ctx, name, nn); err != nil {
			fail(w, 400, err.Error())
			return
		}
		out = nn
	}
	writeJSON(w, 200, map[string]any{"ok": true, "name": out})
}

// first is the sign-in after a reset: a new password, and a new name when
// the reset asked for one. It signs the person out; they sign in again.
func (a *App) first(w http.ResponseWriter, r *http.Request) {
	var b struct{ Name, Password string }
	if readJSON(r, &b) != nil {
		fail(w, 400, "Send a new password.")
		return
	}
	ctx, u := r.Context(), userFrom(r)
	if u.MustChange == 0 {
		fail(w, 400, "Nothing to change. Use Account to change your password.")
		return
	}
	if err := auth.CheckPassword(b.Password); err != nil {
		fail(w, 400, err.Error())
		return
	}
	name := u.Name
	if u.MustChange == db.ChangeName {
		nn, err := auth.CheckName(b.Name)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if nn == auth.TempPassword {
			fail(w, 400, "Pick your own name, not changeme.")
			return
		}
		if nn != name {
			if err := a.DB.Rename(ctx, name, nn); err != nil {
				fail(w, 400, err.Error())
				return
			}
			name = nn
		}
	}
	if err := a.setPassword(ctx, name, b.Password); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"name": name, "result": "Saved. Sign in as " + name + " with your new password."})
}
