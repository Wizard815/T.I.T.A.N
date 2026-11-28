// Command titan-hands is the only part of T.I.T.A.N that can change
// anything. It holds the Docker socket and runs named operations from its
// own allowlist, for titan only.
//
//	TITAN_HANDS_TOKEN       shared with titan (required)
//	TITAN_HANDS_CONTAINERS  "faith:restart:logs minecraft" (a bare name allows start, restart and logs)
//	TITAN_HANDS_SYNCS       "dnd=http://host/sync wiki=http://host/pull"
//	TITAN_HANDS_PER_HOUR    changing operations per target per hour (default 6)
//	TITAN_HANDS_AMP_URL     AMP's address, e.g. http://AMP:8080 (game servers)
//	TITAN_HANDS_AMP_USER, TITAN_HANDS_AMP_PASSWORD   an AMP user that may start and restart those instances
//	TITAN_HANDS_AMP_INSTANCES  "Enshrouded01" (instance or friendly names)
//	TITAN_HANDS_DISCOVER    0 stops titan listing containers (names, state, ports, titan.* and Unraid web UI labels)
//	TITAN_HANDS_HOST        0 turns off the read-only server view (CPU, memory, disks, GPUs)
//	TITAN_HANDS_DISKS       "array=/host/mnt/user cache=/host/mnt/cache" (mount them read only)
//	TITAN_HANDS_PROC, TITAN_HANDS_SYS  where the host's /proc and /sys are (default /proc, /sys)
//	DOCKER_SOCK             default /var/run/docker.sock
//	TITAN_HANDS_ADDR        default :8111
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/wizard815/titan/internal/hands"
)

func main() {
	tok := os.Getenv("TITAN_HANDS_TOKEN")
	if len(tok) < 16 {
		fail("TITAN_HANDS_TOKEN must be set to a long random string (openssl rand -hex 32), the same in titan")
	}
	cs, err := hands.ParseContainers(os.Getenv("TITAN_HANDS_CONTAINERS"))
	if err != nil {
		fail("TITAN_HANDS_CONTAINERS: " + err.Error())
	}
	ss, err := hands.ParseSyncs(os.Getenv("TITAN_HANDS_SYNCS"))
	if err != nil {
		fail("TITAN_HANDS_SYNCS: " + err.Error())
	}
	per, _ := strconv.Atoi(os.Getenv("TITAN_HANDS_PER_HOUR"))
	sock := os.Getenv("DOCKER_SOCK")
	if sock == "" {
		sock = "/var/run/docker.sock"
	}
	addr := os.Getenv("TITAN_HANDS_ADDR")
	if addr == "" {
		addr = ":8111"
	}
	var amp *hands.AMPRules
	if u := os.Getenv("TITAN_HANDS_AMP_URL"); u != "" {
		amp = &hands.AMPRules{URL: u, User: os.Getenv("TITAN_HANDS_AMP_USER"), Password: os.Getenv("TITAN_HANDS_AMP_PASSWORD"), Instances: hands.ParseInstances(os.Getenv("TITAN_HANDS_AMP_INSTANCES"))}
		if amp.User == "" || amp.Password == "" {
			fail("TITAN_HANDS_AMP_URL is set, so TITAN_HANDS_AMP_USER and TITAN_HANDS_AMP_PASSWORD are needed too")
		}
	}
	disks, err := hands.ParseDisks(os.Getenv("TITAN_HANDS_DISKS"))
	if err != nil {
		fail("TITAN_HANDS_DISKS: " + err.Error())
	}
	host := hands.HostRules{Proc: os.Getenv("TITAN_HANDS_PROC"), Sys: os.Getenv("TITAN_HANDS_SYS"), Disks: disks, Off: os.Getenv("TITAN_HANDS_HOST") == "0"}
	s := &hands.Server{Token: tok, Rules: hands.Rules{Containers: cs, Syncs: ss, PerHour: per, AMP: amp, NoDiscover: os.Getenv("TITAN_HANDS_DISCOVER") == "0", Host: host}, Docker: hands.DockerClient(sock), HTTP: &http.Client{Timeout: 60 * time.Second}}
	names := make([]string, 0, len(cs))
	for n := range cs {
		names = append(names, n)
	}
	slog.Info("titan-hands is up", "addr", addr, "containers", names, "syncs", len(ss), "amp", amp != nil)
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "titan-hands:", msg)
	os.Exit(1)
}
