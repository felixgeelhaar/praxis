package cgroup_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/plugin/cgroup"
)

func TestDetect_NonLinuxAlwaysFalse(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("non-linux only")
	}
	st := cgroup.Detect()
	if st.Available {
		t.Errorf("non-linux must report unavailable: %+v", st)
	}
	if st.Reason == "" {
		t.Error("Reason should explain the fallback")
	}
}

func TestDetectAt_MissingUnifiedMount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	tmp := t.TempDir()
	st := cgroup.DetectAt(filepath.Join(tmp, "missing"), filepath.Join(tmp, "praxis"))
	if st.Available {
		t.Errorf("missing unified mount must report unavailable: %+v", st)
	}
}

func TestDetectAt_MissingPraxisSubtree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	tmp := t.TempDir()
	// Fake the cgroup v2 unified-mount marker but omit the praxis
	// subtree.
	if err := os.WriteFile(filepath.Join(tmp, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := cgroup.DetectAt(tmp, filepath.Join(tmp, "praxis"))
	if st.Available {
		t.Errorf("missing subtree must report unavailable: %+v", st)
	}
}

func TestDetectAt_WritableSubtreeAvailable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "praxis")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	st := cgroup.DetectAt(tmp, root)
	if !st.Available {
		t.Errorf("writable subtree should be available: %+v", st)
	}
	if st.Root != root {
		t.Errorf("Root=%s want %s", st.Root, root)
	}
}

func TestDetectAt_SubtreeIsFileNotDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "cgroup.controllers"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	notADir := filepath.Join(tmp, "praxis")
	if err := os.WriteFile(notADir, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	st := cgroup.DetectAt(tmp, notADir)
	if st.Available {
		t.Errorf("file-not-dir subtree must report unavailable: %+v", st)
	}
}
