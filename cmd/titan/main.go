// Command titan is the T.I.T.A.N board, login and chat.
//
//	titan                         serve (the default)
//	titan check                   check titan.yaml and exit
//	titan user add NAME ROLE      add an account (password from TITAN_PASSWORD or typed)
//	titan user password NAME      set a new password
//	titan user list               list accounts
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wizard815/titan/internal/agent"
	"github.com/wizard815/titan/internal/auth"
	"github.com/wizard815/titan/internal/checks"
	"github.com/wizard815/titan/internal/config"
	"github.com/wizard815/titan/internal/db"
	"github.com/wizard815/titan/internal/hands"
	"github.com/wizard815/titan/internal/web"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "titan:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfgPath := env("TITAN_CONFIG", "/config/titan.yaml")
	dataPath := env("TITAN_DATA", "/data/titan.db")
	if len(args) > 0 && args[0] == "check" {
		c, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		fmt.Printf("titan.yaml is fine: %d services, models %s / %s / %s\n", len(c.Services), c.Models.Guard, c.Models.Agent, c.Models.Fallback)
		return nil
	}
	d, err := db.Open(dataPath)
	if err != nil {
		return err
	}
	a := &auth.Auth{DB: d}
	if len(args) > 0 && args[0] == "user" {
		return userCmd(a, d, args[1:])
	}
	if len(args) > 0 {
		return fmt.Errorf("unknown command %q (try check or user)", args[0])
	}
	return serve(cfgPath, dataPath, d, a)
}

func password() (string, error) {
	if p := os.Getenv("TITAN_PASSWORD"); p != "" {
		return p, nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password given (set TITAN_PASSWORD or type one)")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func userCmd(a *auth.Auth, d *db.DB, args []string) error {
	ctx := context.Background()
	switch {
	case len(args) == 3 && args[0] == "add":
		pw, err := password()
		if err != nil {
			return err
		}
		name, err := a.AddUser(ctx, args[1], args[2], pw)
		if err != nil {
			return err
		}
		fmt.Printf("Added %s (%s).\n", name, args[2])
	case len(args) == 2 && args[0] == "password":
		pw, err := password()
		if err != nil {
			return err
		}
		if err := auth.CheckPassword(pw); err != nil {
			return err
		}
		h, _ := auth.Hash(pw)
		if err := d.SetPassword(ctx, args[1], h); err != nil {
			return fmt.Errorf("there's no account called %s", args[1])
		}
		fmt.Println("Password set; their old sign-ins have ended.")
	case len(args) == 1 && args[0] == "list":
		us, err := d.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range us {
			fmt.Printf("%-20s %s\n", u.Name, u.Role)
		}
	default:
		return errors.New("use: titan user add NAME friend|family|owner, titan user password NAME, titan user list")
	}
	return nil
}

// salt keeps pseudonyms stable across restarts without being guessable.
func salt(dataPath string) (string, error) {
	if s := os.Getenv("TITAN_SALT"); s != "" {
		return s, nil
	}
	p := filepath.Join(filepath.Dir(dataPath), "titan.salt")
	if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	b := make([]byte, 16)
	rand.Read(b)
	s := hex.EncodeToString(b)
	return s, os.WriteFile(p, []byte(s+"\n"), 0o600)
}

func serve(cfgPath, dataPath string, d *db.DB, a *auth.Auth) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if n, _ := d.CountUsers(ctx); n == 0 {
		name, pw := os.Getenv("TITAN_FIRST_OWNER"), os.Getenv("TITAN_FIRST_PASSWORD")
		if name == "" || pw == "" {
			slog.Warn("no accounts yet: set TITAN_FIRST_OWNER and TITAN_FIRST_PASSWORD, or run `titan user add wizard owner`")
		} else if _, err := a.AddUser(ctx, name, config.Owner, pw); err != nil {
			return fmt.Errorf("first owner: %w", err)
		} else {
			slog.Info("made the first owner account", "name", name)
		}
	}
	s, err := salt(dataPath)
	if err != nil {
		return err
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	hd := &hands.Client{URL: cfg.Hands.URL, Token: os.Getenv(cfg.Hands.TokenEnv)}
	if hd.URL != "" && hd.Token == "" {
		return fmt.Errorf("hands.url is set but %s is empty: set the same token in both containers", cfg.Hands.TokenEnv)
	}
	models := &agent.Models{URL: cfg.Argos.URL, HTTP: &http.Client{}, Timeout: cfg.Argos.Timeout.D()}
	if cfg.Argos.APIKeyEnv != "" {
		models.APIKey = os.Getenv(cfg.Argos.APIKeyEnv)
	}
	runner := &checks.Runner{Cfg: cfg, DB: d, Hands: hd, HTTP: hc}
	app := &web.App{
		Cfg: cfg, DB: d, Auth: a, Runner: runner, Hands: hd, HTTP: hc,
		ExportToken: os.Getenv("TITAN_EXPORT_TOKEN"), Salt: s,
		OwnerName: env("TITAN_OWNER_LABEL", "Wizard"), Secure: os.Getenv("TITAN_SECURE_COOKIE") == "1",
	}
	app.Agent = &agent.Agent{Models: models, Guard: cfg.Models.Guard, Helper: cfg.Models.Agent, Fallback: cfg.Models.Fallback, Tools: app, Owner: app.OwnerName, Plain: cfg.Style == "plain"}
	runner.OnChange = app.OnChange
	go runner.Run(ctx)

	addr := env("TITAN_ADDR", ":8130")
	srv := &http.Server{Addr: addr, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sh)
	}()
	slog.Info("T.I.T.A.N is up", "addr", addr, "services", len(cfg.Services), "argos", cfg.Argos.URL, "hands", cfg.Hands.URL != "")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	app.Wait()
	return nil
}
