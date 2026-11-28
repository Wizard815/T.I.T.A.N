// Package auth is T.I.T.A.N's own login: scrypt password hashes, a lockout
// after repeated wrong tries, and random session tokens of which only a
// hash is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
)

const (
	MaxTries   = 5
	LockFor    = 15 * time.Minute
	SessionFor = 14 * 24 * time.Hour
	MinPass    = 8
)

var (
	ErrBadLogin = errors.New("That name and password don't match. Check the spelling, or ask Wizard to reset your password.")
	ErrResetOld = errors.New("That reset ran out (they last a day). Ask Wizard to reset it again.")
	ErrLocked   = errors.New("Too many wrong tries. Wait 15 minutes, or ask Wizard to reset your password.")
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
)

// scrypt parameters (interactive login strength).
const sN, sR, sP, keyLen = 1 << 15, 8, 1, 32

func Hash(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k, err := scrypt.Key([]byte(pw), salt, sN, sR, sP, keyLen)
	if err != nil {
		return "", err
	}
	e := base64.RawStdEncoding
	return fmt.Sprintf("scrypt$%d$%d$%d$%s$%s", sN, sR, sP, e.EncodeToString(salt), e.EncodeToString(k)), nil
}

func Verify(hash, pw string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 6 || p[0] != "scrypt" {
		return false
	}
	n, _ := strconv.Atoi(p[1])
	r, _ := strconv.Atoi(p[2])
	q, _ := strconv.Atoi(p[3])
	e := base64.RawStdEncoding
	salt, err1 := e.DecodeString(p[4])
	want, err2 := e.DecodeString(p[5])
	if err1 != nil || err2 != nil || n == 0 {
		return false
	}
	got, err := scrypt.Key([]byte(pw), salt, n, r, q, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummy keeps a login for an unknown name as slow as a real one.
var dummy, _ = Hash("not-a-real-password")

// CheckName returns the cleaned account name or a plain error.
func CheckName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !namePattern.MatchString(name) {
		return "", errors.New("Names are 2-32 lowercase letters, digits, - or _, starting with a letter (like sam).")
	}
	return name, nil
}

// TempPassword is what an owner's reset sets. It only works until the
// person picks their own, and for a day at most.
const TempPassword = "changeme"

// ResetFor is how long a reset's "changeme" works.
const ResetFor = 24 * time.Hour

func CheckPassword(pw string) error {
	if strings.EqualFold(strings.TrimSpace(pw), TempPassword) {
		return errors.New("Pick your own password, not changeme.")
	}
	if len([]rune(pw)) < MinPass {
		return fmt.Errorf("Use a password of at least %d characters.", MinPass)
	}
	return nil
}

type Auth struct{ DB *db.DB }

// AddUser makes an account.
func (a *Auth) AddUser(ctx context.Context, name, role, pw string) (string, error) {
	name, err := CheckName(name)
	if err != nil {
		return "", err
	}
	if !config.ValidRole(role) {
		return "", errors.New("Role must be friend, family or owner.")
	}
	if err := CheckPassword(pw); err != nil {
		return "", err
	}
	h, err := Hash(pw)
	if err != nil {
		return "", err
	}
	return name, a.DB.AddUser(ctx, name, role, h)
}

// Login checks a name and password and returns a new session token.
func (a *Auth) Login(ctx context.Context, name, pw string) (string, *db.User, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	u, err := a.DB.GetUser(ctx, name)
	if errors.Is(err, db.ErrNotFound) {
		Verify(dummy, pw)
		return "", nil, ErrBadLogin
	}
	if err != nil {
		return "", nil, err
	}
	now := time.Now()
	if u.LockedUntil > now.Unix() {
		return "", nil, ErrLocked
	}
	if !Verify(u.Hash, pw) {
		f := u.Failed + 1
		var until int64
		if f >= MaxTries {
			until, f = now.Add(LockFor).Unix(), 0
		}
		_ = a.DB.SetFailed(ctx, u.Name, f, until)
		if until > 0 {
			return "", nil, ErrLocked
		}
		return "", nil, ErrBadLogin
	}
	if u.MustChange > 0 && u.ResetUntil > 0 && now.Unix() > u.ResetUntil {
		return "", nil, ErrResetOld
	}
	if u.Failed > 0 || u.LockedUntil > 0 {
		_ = a.DB.SetFailed(ctx, u.Name, 0, 0)
	}
	tok := NewToken()
	if err := a.DB.AddSession(ctx, HashToken(tok), u.Name, now.Add(SessionFor)); err != nil {
		return "", nil, err
	}
	return tok, u, nil
}

func (a *Auth) Session(ctx context.Context, tok string) (*db.User, error) {
	if tok == "" {
		return nil, db.ErrNotFound
	}
	return a.DB.SessionUser(ctx, HashToken(tok))
}

func (a *Auth) Logout(ctx context.Context, tok string) error {
	return a.DB.DropSession(ctx, HashToken(tok))
}

func NewToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func HashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// Pseudonym is a stable id for an account in events, so Overseer's training
// data never carries a family member's name.
func Pseudonym(salt, name string) string {
	s := sha256.Sum256([]byte("titan-person:" + salt + ":" + name))
	return "p_" + hex.EncodeToString(s[:6])
}
