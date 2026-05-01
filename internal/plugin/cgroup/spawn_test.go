package cgroup_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/plugin/cgroup"
)

func TestPrepare_WritesMemoryAndCPULimits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	parent := t.TempDir()
	h, err := cgroup.Prepare(parent, "p1", cgroup.Budget{
		MaxMemoryBytes: 100 << 20, // 100 MiB
		CPUTimeout:     30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if h.Path != filepath.Join(parent, "p1") {
		t.Errorf("Path=%s", h.Path)
	}
	memMax, err := os.ReadFile(filepath.Join(h.Path, "memory.max"))
	if err != nil {
		t.Fatalf("read memory.max: %v", err)
	}
	if string(memMax) != "104857600" {
		t.Errorf("memory.max=%q want 104857600", memMax)
	}
	cpuMax, err := os.ReadFile(filepath.Join(h.Path, "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if string(cpuMax) != "30000000 100000" {
		t.Errorf("cpu.max=%q", cpuMax)
	}
}

func TestPrepare_SkipsZeroFields(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	parent := t.TempDir()
	h, err := cgroup.Prepare(parent, "p2", cgroup.Budget{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.Path, "memory.max")); err == nil {
		t.Error("memory.max written despite zero MaxMemoryBytes")
	}
	if _, err := os.Stat(filepath.Join(h.Path, "cpu.max")); err == nil {
		t.Error("cpu.max written despite zero CPUTimeout")
	}
}

func TestPrepare_RejectsEmptyArgs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	if _, err := cgroup.Prepare("", "x", cgroup.Budget{}); err == nil {
		t.Error("empty parent must error")
	}
	if _, err := cgroup.Prepare("/tmp", "", cgroup.Budget{}); err == nil {
		t.Error("empty name must error")
	}
}

func TestAddPID_WritesToCgroupProcs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	parent := t.TempDir()
	h, _ := cgroup.Prepare(parent, "p3", cgroup.Budget{})
	if err := h.AddPID(12345); err != nil {
		t.Fatalf("AddPID: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(h.Path, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "12345" {
		t.Errorf("cgroup.procs=%q want 12345", got)
	}
}

func TestCleanup_RemovesCgroupDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	parent := t.TempDir()
	h, _ := cgroup.Prepare(parent, "p4", cgroup.Budget{})
	// Real /sys/fs/cgroup files live at the kernel; in tests they
	// are plain regular files we created via writeCgroupFile. To
	// allow Remove() to succeed, drop the limit files first — the
	// kernel handles this implicitly on a real system.
	for _, f := range []string{"memory.max", "cpu.max"} {
		_ = os.Remove(filepath.Join(h.Path, f))
	}
	_ = os.Remove(filepath.Join(h.Path, "cgroup.procs"))
	if err := h.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(h.Path); !os.IsNotExist(err) {
		t.Errorf("cgroup directory still present: %v", err)
	}
}

func TestCleanup_NilHandleNoOp(t *testing.T) {
	var h *cgroup.Handle
	if err := h.Cleanup(); err != nil {
		t.Errorf("nil handle Cleanup: %v", err)
	}
}

func TestSanitize_RejectsSlashesAndControl(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	parent := t.TempDir()
	// "evil/../escape" must be sanitised so the resulting path
	// stays under parent.
	h, err := cgroup.Prepare(parent, "evil/../escape", cgroup.Budget{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	rel, err := filepath.Rel(parent, h.Path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(rel) != "." {
		t.Errorf("path escaped parent: rel=%s", rel)
	}
}
