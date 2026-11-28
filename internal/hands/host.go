package hands

// host: a read-only look at the server itself, like btop: CPU, memory,
// disks and GPUs. It reads /proc and /sys (a container sees the host's CPU
// and memory there) and statfs on the disks the owner mounts in read only.
// No commands run.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const OpHost = "host"

type Host struct {
	CPU      float64 `json:"cpu_percent"`
	Cores    int     `json:"cores"`
	Load     float64 `json:"load_1m"`
	MemUsed  uint64  `json:"mem_used"`
	MemTotal uint64  `json:"mem_total"`
	Uptime   int64   `json:"uptime_seconds"`
	Disks    []Disk  `json:"disks"`
	GPUs     []GPU   `json:"gpus"`
}

type Disk struct {
	Name  string `json:"name"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

type GPU struct {
	Name     string  `json:"name"`
	Busy     float64 `json:"busy_percent"`
	MemUsed  uint64  `json:"mem_used"`
	MemTotal uint64  `json:"mem_total"`
	Temp     float64 `json:"temp_c,omitempty"`
}

// HostRules says where to look. Disks maps a label to a mounted path.
type HostRules struct {
	Proc  string            // default /proc
	Sys   string            // default /sys
	Disks map[string]string // "array" -> /host/mnt/user
	Off   bool
}

// ParseDisks reads "array=/host/mnt/user cache=/host/mnt/cache".
func ParseDisks(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, e := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" || !filepath.IsAbs(v) {
			return nil, fmt.Errorf("%q should look like array=/host/mnt/user", e)
		}
		out[k] = v
	}
	return out, nil
}

func (s *Server) host(ctx context.Context) (Reply, int) {
	h := s.Rules.Host
	if h.Off {
		return Reply{Message: "titan-hands' server view is off (TITAN_HANDS_HOST=0)."}, 403
	}
	proc, sys := or(h.Proc, "/proc"), or(h.Sys, "/sys")
	var out Host
	a, err := cpuTimes(proc)
	if err != nil {
		return Reply{Message: "Can't read the CPU: " + err.Error()}, 502
	}
	select {
	case <-time.After(300 * time.Millisecond):
	case <-ctx.Done():
		return Reply{Message: "cancelled"}, 499
	}
	b, err := cpuTimes(proc)
	if err == nil && b[1] > a[1] {
		out.CPU = round1(100 * (1 - float64(b[0]-a[0])/float64(b[1]-a[1])))
	}
	out.Cores = cores(proc)
	if f, err := os.ReadFile(filepath.Join(proc, "loadavg")); err == nil {
		out.Load, _ = strconv.ParseFloat(strings.Fields(string(f))[0], 64)
	}
	if f, err := os.ReadFile(filepath.Join(proc, "uptime")); err == nil {
		u, _ := strconv.ParseFloat(strings.Fields(string(f))[0], 64)
		out.Uptime = int64(u)
	}
	out.MemUsed, out.MemTotal = memory(proc)
	names := make([]string, 0, len(h.Disks))
	for n := range h.Disks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var st syscall.Statfs_t
		if err := syscall.Statfs(h.Disks[n], &st); err != nil {
			continue
		}
		total := st.Blocks * uint64(st.Bsize)
		out.Disks = append(out.Disks, Disk{Name: n, Total: total, Used: total - st.Bavail*uint64(st.Bsize)})
	}
	out.GPUs = gpus(sys)
	if out.Disks == nil {
		out.Disks = []Disk{}
	}
	if out.GPUs == nil {
		out.GPUs = []GPU{}
	}
	return Reply{OK: true, Host: &out}, 200
}

// cpuTimes is [idle+iowait, total] from the first line of /proc/stat.
func cpuTimes(proc string) ([2]uint64, error) {
	f, err := os.Open(filepath.Join(proc, "stat"))
	if err != nil {
		return [2]uint64{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return [2]uint64{}, fmt.Errorf("empty")
	}
	fs := strings.Fields(sc.Text())
	if len(fs) < 6 || fs[0] != "cpu" {
		return [2]uint64{}, fmt.Errorf("unexpected format")
	}
	var t [2]uint64
	for i, v := range fs[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		if i < 8 { // user..steal; guest is already in user
			t[1] += n
		}
		if i == 3 || i == 4 {
			t[0] += n
		}
	}
	return t, nil
}

func cores(proc string) int {
	f, err := os.ReadFile(filepath.Join(proc, "stat"))
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(string(f), "\n") {
		if strings.HasPrefix(l, "cpu") && len(l) > 3 && l[3] >= '0' && l[3] <= '9' {
			n++
		}
	}
	return n
}

func memory(proc string) (used, total uint64) {
	f, err := os.ReadFile(filepath.Join(proc, "meminfo"))
	if err != nil {
		return 0, 0
	}
	kv := map[string]uint64{}
	for _, l := range strings.Split(string(f), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.Fields(v + " 0")[0], 10, 64)
		kv[k] = n * 1024
	}
	total = kv["MemTotal"]
	if avail, ok := kv["MemAvailable"]; ok && avail <= total {
		return total - avail, total
	}
	return 0, total
}

// gpus reads AMD cards from sysfs (busy %, VRAM, temperature).
func gpus(sys string) []GPU {
	cards, _ := filepath.Glob(filepath.Join(sys, "class", "drm", "card[0-9]*"))
	sort.Strings(cards)
	var out []GPU
	for _, c := range cards {
		if strings.Contains(filepath.Base(c), "-") { // card0-DP-1 is a connector
			continue
		}
		dev := filepath.Join(c, "device")
		total := readUint(filepath.Join(dev, "mem_info_vram_total"))
		if total == 0 {
			continue
		}
		g := GPU{Name: "GPU " + strings.TrimPrefix(filepath.Base(c), "card"), MemTotal: total, MemUsed: readUint(filepath.Join(dev, "mem_info_vram_used")), Busy: float64(readUint(filepath.Join(dev, "gpu_busy_percent")))}
		if t, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*", "temp1_input")); len(t) > 0 {
			g.Temp = float64(readUint(t[0])) / 1000
		}
		out = append(out, g)
	}
	return out
}

func readUint(p string) uint64 {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func or(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// Host asks titan-hands for the server view.
func (c *Client) Host(ctx context.Context) (*Host, error) {
	rep, err := c.Do(ctx, OpHost, "")
	if err != nil {
		return nil, err
	}
	if rep.Host == nil {
		return nil, fmt.Errorf("titan-hands sent no server view")
	}
	return rep.Host, nil
}
