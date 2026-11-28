package hands

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHands_HostReadsProcSysAndDisks(t *testing.T) {
	root := t.TempDir()
	w := func(p, s string) {
		p = filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	w("proc/stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400 0 0 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0 0 0\n")
	w("proc/meminfo", "MemTotal:       1000 kB\nMemFree:         100 kB\nMemAvailable:    400 kB\n")
	w("proc/loadavg", "1.50 1.00 0.50 1/100 1\n")
	w("proc/uptime", "3600.5 100\n")
	w("sys/class/drm/card0/device/mem_info_vram_total", "34359738368\n")
	w("sys/class/drm/card0/device/mem_info_vram_used", "17179869184\n")
	w("sys/class/drm/card0/device/gpu_busy_percent", "42\n")
	w("sys/class/drm/card0/device/hwmon/hwmon3/temp1_input", "55000\n")
	w("sys/class/drm/card0-DP-1/status", "connected\n")
	w("sys/class/drm/card1/device/vendor", "0x8086\n") // no VRAM: not listed
	s := &Server{Rules: Rules{Host: HostRules{Proc: filepath.Join(root, "proc"), Sys: filepath.Join(root, "sys"), Disks: map[string]string{"array": root}}}}
	rep, code := s.Do(context.Background(), Request{Op: OpHost})
	if code != 200 || rep.Host == nil {
		t.Fatalf("host: %d %+v", code, rep)
	}
	h := rep.Host
	if h.Cores != 2 || h.MemTotal != 1000*1024 || h.MemUsed != 600*1024 || h.Load != 1.5 || h.Uptime != 3600 {
		t.Fatalf("cpu/mem: %+v", h)
	}
	if len(h.GPUs) != 1 || h.GPUs[0].Busy != 42 || h.GPUs[0].Temp != 55 || h.GPUs[0].MemUsed*2 != h.GPUs[0].MemTotal {
		t.Fatalf("gpus: %+v", h.GPUs)
	}
	if len(h.Disks) != 1 || h.Disks[0].Total == 0 {
		t.Fatalf("disks: %+v", h.Disks)
	}
	s.Rules.Host.Off = true
	if _, code := s.Do(context.Background(), Request{Op: OpHost}); code != 403 {
		t.Fatalf("off: %d", code)
	}
}
