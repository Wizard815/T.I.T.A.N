// Package db is T.I.T.A.N's own SQLite file: accounts, sessions, check
// results (for uptime), fix proposals, the owner's inbox, and the event
// log that Overseer reads.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type DB struct{ *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS users (
  name TEXT PRIMARY KEY,
  role TEXT NOT NULL,
  pw_hash TEXT NOT NULL,
  failed INTEGER NOT NULL DEFAULT 0,
  locked_until INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS grants (
  name TEXT NOT NULL,            -- an account that runs one service
  service TEXT NOT NULL,
  PRIMARY KEY (name, service)
);
CREATE TABLE IF NOT EXISTS sessions (
  hash TEXT PRIMARY KEY,
  name TEXT NOT NULL REFERENCES users(name) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS results (
  service TEXT NOT NULL,
  at INTEGER NOT NULL,
  ok INTEGER NOT NULL,
  ms INTEGER NOT NULL,
  detail TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS results_service_at ON results(service, at);
CREATE TABLE IF NOT EXISTS proposals (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  service TEXT NOT NULL,
  action TEXT NOT NULL,
  state TEXT NOT NULL,          -- pending | done | failed | declined | expired
  result TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  decided_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS inbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  kind TEXT NOT NULL,           -- refused | ask_owner | tell_wizard | down
  name TEXT NOT NULL,
  text TEXT NOT NULL,
  done INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS updates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service TEXT NOT NULL,
  at INTEGER NOT NULL,
  author TEXT NOT NULL,          -- an account name, or "titan"
  bot INTEGER NOT NULL DEFAULT 0,
  text TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS updates_service ON updates(service, id);
CREATE TABLE IF NOT EXISTS working (
  service TEXT NOT NULL,
  name TEXT NOT NULL,
  since INTEGER NOT NULL,
  PRIMARY KEY (service, name)
);
CREATE TABLE IF NOT EXISTS room (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  author TEXT NOT NULL,          -- an account name, or "titan"
  bot INTEGER NOT NULL DEFAULT 0,
  text TEXT NOT NULL,
  for_name TEXT NOT NULL DEFAULT '',  -- whose Yes the proposals wait for
  proposals TEXT NOT NULL DEFAULT '',
  removed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS quiet (
  service TEXT PRIMARY KEY,     -- a service id, or "*" for everything
  until INTEGER NOT NULL,       -- unix time; 0 = until the owner lifts it
  by TEXT NOT NULL,
  why TEXT NOT NULL DEFAULT '',
  since INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  kind TEXT NOT NULL,           -- state | guard | action | conversation
  data TEXT NOT NULL
);
`

// Open opens (and if needed creates) the database file.
func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	s, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		s.SetMaxOpenConns(1)
	}
	if _, err := s.Exec(schema); err != nil {
		return nil, fmt.Errorf("can't set up the database at %s: %w", path, err)
	}
	// Columns added later; each fails harmlessly when it's already there.
	s.Exec(`ALTER TABLE quiet ADD COLUMN since INTEGER NOT NULL DEFAULT 0`)
	s.Exec(`ALTER TABLE users ADD COLUMN must_change INTEGER NOT NULL DEFAULT 0`)
	s.Exec(`ALTER TABLE users ADD COLUMN reset_until INTEGER NOT NULL DEFAULT 0`)
	return &DB{s}, nil
}

// ---- accounts ----

type User struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	Hash        string `json:"-"`
	Failed      int    `json:"-"`
	LockedUntil int64  `json:"-"`
	CreatedAt   int64  `json:"created_at"`
	// MustChange is set by an owner's reset: 1 = pick a new password at the
	// next sign-in, 2 = a new name too. ResetUntil is when "changeme" stops
	// working, so a forgotten reset can't be picked up by someone else later.
	MustChange int      `json:"must_change"`
	ResetUntil int64    `json:"-"`
	Manages    []string `json:"manages"` // services this account runs (see grants)
}

// Must-change modes.
const (
	ChangePassword = 1
	ChangeName     = 2 // a new name and a new password
)

func (d *DB) AddUser(ctx context.Context, name, role, hash string) error {
	_, err := d.ExecContext(ctx, `INSERT INTO users (name, role, pw_hash, created_at) VALUES (?, ?, ?, ?)`, name, role, hash, time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("there's already an account called %s", name)
	}
	return err
}

func (d *DB) GetUser(ctx context.Context, name string) (*User, error) {
	u := &User{}
	err := d.QueryRowContext(ctx, `SELECT name, role, pw_hash, failed, locked_until, created_at, must_change, reset_until FROM users WHERE name = ?`, name).
		Scan(&u.Name, &u.Role, &u.Hash, &u.Failed, &u.LockedUntil, &u.CreatedAt, &u.MustChange, &u.ResetUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (d *DB) Users(ctx context.Context) ([]User, error) {
	rows, err := d.QueryContext(ctx, `SELECT name, role, created_at, must_change FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Name, &u.Role, &u.CreatedAt, &u.MustChange); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) SetFailed(ctx context.Context, name string, failed int, lockedUntil int64) error {
	_, err := d.ExecContext(ctx, `UPDATE users SET failed = ?, locked_until = ? WHERE name = ?`, failed, lockedUntil, name)
	return err
}

func (d *DB) SetPassword(ctx context.Context, name, hash string) error {
	r, err := d.ExecContext(ctx, `UPDATE users SET pw_hash = ?, failed = 0, locked_until = 0, must_change = 0, reset_until = 0 WHERE name = ?`, hash, name)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = d.ExecContext(ctx, `DELETE FROM sessions WHERE name = ?`, name)
	return err
}

func (d *DB) SetRole(ctx context.Context, name, role string) error {
	r, err := d.ExecContext(ctx, `UPDATE users SET role = ? WHERE name = ?`, role, name)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) RemoveUser(ctx context.Context, name string) error {
	d.ExecContext(ctx, `DELETE FROM grants WHERE name = ?`, name)
	r, err := d.ExecContext(ctx, `DELETE FROM users WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Reset puts a temporary password hash on an account and signs it out
// everywhere. mode is ChangePassword or ChangeName.
func (d *DB) Reset(ctx context.Context, name, hash string, mode int, until time.Time) error {
	r, err := d.ExecContext(ctx, `UPDATE users SET pw_hash = ?, must_change = ?, reset_until = ?, failed = 0, locked_until = 0 WHERE name = ?`, hash, mode, until.Unix(), name)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = d.ExecContext(ctx, `DELETE FROM sessions WHERE name = ?`, name)
	return err
}

// Rename moves an account, and everything that names it, to a new name.
// Its sessions end: sign in again with the new name.
func (d *DB) Rename(ctx context.Context, old, name string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE name = ?`, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("there's already an account called %s", name)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE name = ?`, old); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE users SET name = ? WHERE name = ?`, name, old)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	for _, q := range []string{
		`UPDATE grants SET name = ? WHERE name = ?`,
		`UPDATE working SET name = ? WHERE name = ?`,
		`UPDATE proposals SET name = ? WHERE name = ?`,
		`UPDATE updates SET author = ? WHERE author = ?`,
		`UPDATE room SET author = ? WHERE author = ?`,
		`UPDATE room SET for_name = ? WHERE for_name = ?`,
		`UPDATE quiet SET by = ? WHERE by = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, name, old); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Grants lists the services an account runs.
func (d *DB) Grants(ctx context.Context, name string) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT service FROM grants WHERE name = ? ORDER BY service`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetGrants replaces the services an account runs.
func (d *DB) SetGrants(ctx context.Context, name string, services []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM grants WHERE name = ?`, name); err != nil {
		return err
	}
	for _, s := range services {
		if _, err := tx.ExecContext(ctx, `INSERT INTO grants (name, service) VALUES (?, ?) ON CONFLICT DO NOTHING`, name, s); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- sessions (only the hash is stored) ----

func (d *DB) AddSession(ctx context.Context, hash, name string, expires time.Time) error {
	_, err := d.ExecContext(ctx, `INSERT INTO sessions (hash, name, expires_at) VALUES (?, ?, ?)`, hash, name, expires.Unix())
	return err
}

func (d *DB) SessionUser(ctx context.Context, hash string) (*User, error) {
	u := &User{}
	err := d.QueryRowContext(ctx, `SELECT u.name, u.role, u.must_change FROM sessions s JOIN users u ON u.name = s.name WHERE s.hash = ? AND s.expires_at > ?`, hash, time.Now().Unix()).
		Scan(&u.Name, &u.Role, &u.MustChange)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (d *DB) DropSession(ctx context.Context, hash string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash)
	return err
}

// ---- check results ----

type Result struct {
	At     int64  `json:"at"`
	OK     bool   `json:"ok"`
	MS     int64  `json:"ms"`
	Detail string `json:"detail"`
}

func (d *DB) AddResult(ctx context.Context, service string, r Result) error {
	_, err := d.ExecContext(ctx, `INSERT INTO results (service, at, ok, ms, detail) VALUES (?, ?, ?, ?, ?)`, service, r.At, b2i(r.OK), r.MS, r.Detail)
	return err
}

// Recent returns the last n results, oldest first.
func (d *DB) Recent(ctx context.Context, service string, n int) ([]Result, error) {
	rows, err := d.QueryContext(ctx, `SELECT at, ok, ms, detail FROM (SELECT rowid AS rid, at, ok, ms, detail FROM results WHERE service = ? ORDER BY at DESC, rowid DESC LIMIT ?) ORDER BY at, rid`, service, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		var ok int
		if err := rows.Scan(&r.At, &ok, &r.MS, &r.Detail); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// Uptime is the share of good checks since t, or -1 with no checks.
func (d *DB) Uptime(ctx context.Context, service string, since time.Time) (float64, error) {
	var total, good sql.NullInt64
	err := d.QueryRowContext(ctx, `SELECT count(*), sum(ok) FROM results WHERE service = ? AND at >= ?`, service, since.Unix()).Scan(&total, &good)
	if err != nil || total.Int64 == 0 {
		return -1, err
	}
	return float64(good.Int64) / float64(total.Int64), nil
}

// Prune drops results older than keep.
func (d *DB) Prune(ctx context.Context, keep time.Duration) error {
	_, err := d.ExecContext(ctx, `DELETE FROM results WHERE at < ?`, time.Now().Add(-keep).Unix())
	if err == nil {
		_, err = d.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	}
	if err == nil {
		_, err = d.ExecContext(ctx, `DELETE FROM events WHERE at < ?`, time.Now().Add(-keep).UnixMilli())
	}
	if err == nil {
		_, err = d.ExecContext(ctx, `DELETE FROM updates WHERE at < ?`, time.Now().Add(-keep).Unix())
	}
	if err == nil {
		_, err = d.ExecContext(ctx, `DELETE FROM room WHERE at < ?`, time.Now().Add(-keep).Unix())
	}
	return err
}

// ---- fix proposals ----

type Proposal struct {
	ID        string `json:"id"`
	Name      string `json:"-"`
	Service   string `json:"service"`
	Action    string `json:"action"`
	State     string `json:"state"`
	Result    string `json:"result,omitempty"`
	CreatedAt int64  `json:"created_at"`
	DecidedAt int64  `json:"decided_at,omitempty"`
}

func (d *DB) AddProposal(ctx context.Context, p Proposal) error {
	_, err := d.ExecContext(ctx, `INSERT INTO proposals (id, name, service, action, state, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`, p.ID, p.Name, p.Service, p.Action, p.CreatedAt)
	return err
}

func (d *DB) GetProposal(ctx context.Context, id string) (*Proposal, error) {
	p := &Proposal{}
	err := d.QueryRowContext(ctx, `SELECT id, name, service, action, state, result, created_at, decided_at FROM proposals WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Service, &p.Action, &p.State, &p.Result, &p.CreatedAt, &p.DecidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// Claim moves a pending proposal to "running" so a double click can't run it
// twice. It reports false when someone already decided it.
func (d *DB) Claim(ctx context.Context, id, state string) (bool, error) {
	r, err := d.ExecContext(ctx, `UPDATE proposals SET state = ?, decided_at = ? WHERE id = ? AND state = 'pending'`, state, time.Now().Unix(), id)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}

func (d *DB) Finish(ctx context.Context, id, state, result string) error {
	_, err := d.ExecContext(ctx, `UPDATE proposals SET state = ?, result = ? WHERE id = ?`, state, result, id)
	return err
}

// DoneSince counts changing actions run on service since t (for limits).
func (d *DB) DoneSince(ctx context.Context, service, action string, since time.Time) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM proposals WHERE service = ? AND action = ? AND state IN ('running', 'done', 'failed') AND decided_at >= ?`, service, action, since.Unix()).Scan(&n)
	return n, err
}

// ---- the owner's inbox ----

type Note struct {
	ID   int64  `json:"id"`
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Text string `json:"text"`
}

func (d *DB) AddNote(ctx context.Context, kind, name, text string) error {
	_, err := d.ExecContext(ctx, `INSERT INTO inbox (at, kind, name, text) VALUES (?, ?, ?, ?)`, time.Now().Unix(), kind, name, text)
	return err
}

func (d *DB) Notes(ctx context.Context) ([]Note, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, at, kind, name, text FROM inbox WHERE done = 0 ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.At, &n.Kind, &n.Name, &n.Text); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *DB) DoneNote(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `UPDATE inbox SET done = 1 WHERE id = ?`, id)
	return err
}

// ---- per-service updates: what happened, and who's on it ----

type Update struct {
	ID      int64  `json:"id"`
	Service string `json:"service"`
	At      int64  `json:"at"`
	Author  string `json:"author"`
	Bot     bool   `json:"bot"`
	Text    string `json:"text"`
}

func (d *DB) AddUpdate(ctx context.Context, u Update) error {
	_, err := d.ExecContext(ctx, `INSERT INTO updates (service, at, author, bot, text) VALUES (?, ?, ?, ?, ?)`, u.Service, time.Now().Unix(), u.Author, b2i(u.Bot), u.Text)
	return err
}

// Updates returns a service's newest updates, newest first.
func (d *DB) Updates(ctx context.Context, service string, limit int) ([]Update, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, service, at, author, bot, text FROM updates WHERE service = ? ORDER BY id DESC LIMIT ?`, service, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Update{}
	for rows.Next() {
		var u Update
		var bot int
		if err := rows.Scan(&u.ID, &u.Service, &u.At, &u.Author, &bot, &u.Text); err != nil {
			return nil, err
		}
		u.Bot = bot == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

// RecentUpdates is the newest updates on any service.
func (d *DB) RecentUpdates(ctx context.Context, limit int) ([]Update, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, service, at, author, bot, text FROM updates ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Update{}
	for rows.Next() {
		var u Update
		var bot int
		if err := rows.Scan(&u.ID, &u.Service, &u.At, &u.Author, &bot, &u.Text); err != nil {
			return nil, err
		}
		u.Bot = bot == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatesSince counts updates per service since t (for the badge).
func (d *DB) UpdatesSince(ctx context.Context, since time.Time) (map[string]int, error) {
	rows, err := d.QueryContext(ctx, `SELECT service, count(*) FROM updates WHERE at >= ? GROUP BY service`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

func (d *DB) SetWorking(ctx context.Context, service, name string, on bool) error {
	var err error
	if on {
		_, err = d.ExecContext(ctx, `INSERT INTO working (service, name, since) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, service, name, time.Now().Unix())
	} else {
		_, err = d.ExecContext(ctx, `DELETE FROM working WHERE service = ? AND name = ?`, service, name)
	}
	return err
}

func (d *DB) ClearWorking(ctx context.Context, service string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM working WHERE service = ?`, service)
	return err
}

// Working lists who is on each service (older than 12 hours drops off).
func (d *DB) Working(ctx context.Context) (map[string][]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT service, name FROM working WHERE since >= ? ORDER BY since`, time.Now().Add(-12*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var s, n string
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = append(out[s], n)
	}
	return out, rows.Err()
}

// ---- quiet: the owner is working on something, so don't fix or alert ----

type Quiet struct {
	Service string `json:"service"`
	Until   int64  `json:"until"` // 0 = until lifted
	By      string `json:"by"`
	Why     string `json:"why"`
	Since   int64  `json:"since"`
}

func (d *DB) SetQuiet(ctx context.Context, q Quiet) error {
	_, err := d.ExecContext(ctx, `INSERT INTO quiet (service, until, by, why, since) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(service) DO UPDATE SET until = excluded.until, by = excluded.by, why = excluded.why, since = excluded.since`, q.Service, q.Until, q.By, q.Why, time.Now().Unix())
	return err
}

func (d *DB) LiftQuiet(ctx context.Context, service string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM quiet WHERE service = ?`, service)
	return err
}

// Quiets lists what is quiet now, by service ("*" = everything).
func (d *DB) Quiets(ctx context.Context) (map[string]Quiet, error) {
	now := time.Now().Unix()
	if _, err := d.ExecContext(ctx, `DELETE FROM quiet WHERE until > 0 AND until <= ?`, now); err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, `SELECT service, until, by, why, since FROM quiet`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Quiet{}
	for rows.Next() {
		var q Quiet
		if err := rows.Scan(&q.Service, &q.Until, &q.By, &q.Why, &q.Since); err != nil {
			return nil, err
		}
		out[q.Service] = q
	}
	return out, rows.Err()
}

// FailedSince counts failed checks of service since t, and all checks.
func (d *DB) FailedSince(ctx context.Context, service string, t time.Time) (failed, all int, err error) {
	err = d.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN ok = 0 THEN 1 ELSE 0 END), 0), count(*) FROM results WHERE service = ? AND at >= ?`, service, t.Unix()).Scan(&failed, &all)
	return
}

// ---- the room: one chat everyone signed in shares ----

type Line struct {
	ID        int64           `json:"id"`
	At        int64           `json:"at"`
	Author    string          `json:"author"`
	Bot       bool            `json:"bot"`
	Text      string          `json:"text"`
	For       string          `json:"-"`
	Proposals json.RawMessage `json:"proposals,omitempty"`
	Removed   bool            `json:"removed,omitempty"`
}

func (d *DB) AddLine(ctx context.Context, l Line) (int64, error) {
	r, err := d.ExecContext(ctx, `INSERT INTO room (at, author, bot, text, for_name, proposals) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), l.Author, b2i(l.Bot), l.Text, l.For, string(l.Proposals))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Lines returns lines after id, oldest first. With after 0 it returns the
// newest limit lines.
func (d *DB) Lines(ctx context.Context, after int64, limit int) ([]Line, error) {
	q := `SELECT id, at, author, bot, text, for_name, proposals, removed FROM room WHERE id > ? ORDER BY id LIMIT ?`
	if after == 0 {
		q = `SELECT * FROM (SELECT id, at, author, bot, text, for_name, proposals, removed FROM room ORDER BY id DESC LIMIT ?) ORDER BY id`
	}
	var rows *sql.Rows
	var err error
	if after == 0 {
		rows, err = d.QueryContext(ctx, q, limit)
	} else {
		rows, err = d.QueryContext(ctx, q, after, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Line{}
	for rows.Next() {
		var l Line
		var bot, removed int
		var props string
		if err := rows.Scan(&l.ID, &l.At, &l.Author, &bot, &l.Text, &l.For, &props, &removed); err != nil {
			return nil, err
		}
		l.Bot, l.Removed = bot == 1, removed == 1
		if props != "" {
			l.Proposals = json.RawMessage(props)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RemoveLine blanks a line (the owner's moderation); the line stays so the
// log keeps its order.
func (d *DB) RemoveLine(ctx context.Context, id int64) error {
	r, err := d.ExecContext(ctx, `UPDATE room SET text = '', proposals = '', removed = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- events for Overseer ----

type Event struct {
	ID   int64           `json:"id"`
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func (d *DB) AddEvent(ctx context.Context, kind string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = d.ExecContext(ctx, `INSERT INTO events (at, kind, data) VALUES (?, ?, ?)`, time.Now().UnixMilli(), kind, string(b))
	return err
}

func (d *DB) Events(ctx context.Context, afterID int64, limit int) ([]Event, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, at, kind, data FROM events WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at int64
		var data string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &data); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at).UTC()
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
