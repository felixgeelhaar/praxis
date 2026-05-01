//go:build linux

package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRootDir is the conventional location of the praxis-managed
// cgroup subtree. Operators can override via DetectAt for tests or
// non-standard layouts.
const DefaultRootDir = "/sys/fs/cgroup/praxis"

// UnifiedMount is the canonical cgroup v2 root on systemd hosts.
const UnifiedMount = "/sys/fs/cgroup"

// Detect probes the conventional locations and reports whether
// Praxis can use cgroup v2 enforcement on this host. Returns
// Status.Available=false (with a populated Reason) on:
//
//   - non-Linux platforms (the other-OS file short-circuits this);
//   - cgroup v1 hosts (cgroup.controllers absent);
//   - hosts where the praxis subtree does not exist;
//   - hosts where the praxis subtree is not writable by the runtime user.
//
// Detection is fast (a few stat / read calls) and never blocks.
func Detect() Status { return DetectAt(UnifiedMount, DefaultRootDir) }

// DetectAt is Detect parameterised on the unified mount and root
// directory paths. Used by tests with a tmpdir mock.
func DetectAt(unified, root string) Status {
	if _, err := os.Stat(filepath.Join(unified, "cgroup.controllers")); err != nil {
		return Status{
			UnifiedMount: unified,
			Reason:       fmt.Sprintf("cgroup v2 unified mount not found: %v", err),
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		return Status{
			UnifiedMount: unified,
			Reason:       fmt.Sprintf("praxis cgroup subtree %s not found: %v (delegate it with systemd-run or chown to the runtime user)", root, err),
		}
	}
	if !info.IsDir() {
		return Status{
			UnifiedMount: unified,
			Reason:       fmt.Sprintf("%s exists but is not a directory", root),
		}
	}
	// Writability probe: create + remove a test entry. cgroup.subtree_control
	// is the canonical test target but writes to it are operator-controlled;
	// stat plus a temp-file create probe is safer for a startup check.
	probe, err := os.CreateTemp(root, ".praxis-detect-")
	if err != nil {
		return Status{
			UnifiedMount: unified,
			Root:         root,
			Reason:       fmt.Sprintf("praxis cgroup subtree %s not writable: %v", root, err),
		}
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)

	return Status{
		Available:    true,
		Root:         root,
		UnifiedMount: unified,
	}
}
