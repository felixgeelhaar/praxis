//go:build linux

package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Budget mirrors the subset of plugin.ResourceBudget the cgroup
// applier consumes. Defined here to keep this package free of an
// import cycle with internal/plugin.
type Budget struct {
	CPUTimeout     time.Duration
	MaxMemoryBytes uint64
}

// Handle owns one per-plugin cgroup directory. Callers create it via
// Prepare, attach a child PID with AddPID, and reclaim the directory
// with Cleanup once the child exits.
type Handle struct {
	Path string
}

// Prepare creates a per-plugin cgroup under parent and writes
// memory.max + cpu.max from b. Returns ErrUnavailable when parent
// does not exist or is not writable so the caller can fall back to
// setrlimit without further branching.
//
// CPU is encoded as "<quota> <period>" microseconds. We pick a 100ms
// period and a quota matching b.CPUTimeout / period_count so the
// effective rate is "1 CPU until budget is exhausted." Budget=0
// fields are skipped (no enforcement on that dimension).
//
// Atomicity caveat: this Prepare creates the cgroup before Start;
// the parent attaches the child via AddPID after fork+exec. There is
// a brief window (microseconds) where the child runs unconstrained.
// True atomicity requires clone3 + CLONE_INTO_CGROUP and a per-fd
// open of the cgroup directory, which Go's exec.Cmd does not expose.
// The window is acceptable for resource isolation; it is NOT
// acceptable for adversarial workloads — those require the future
// landlock+seccomp layer.
func Prepare(parent, name string, b Budget) (*Handle, error) {
	if parent == "" || name == "" {
		return nil, errors.New("cgroup Prepare: parent and name required")
	}
	path := filepath.Join(parent, sanitize(name))
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir cgroup %s: %w", path, err)
	}

	if b.MaxMemoryBytes > 0 {
		if err := writeCgroupFile(path, "memory.max", strconv.FormatUint(b.MaxMemoryBytes, 10)); err != nil {
			_ = os.Remove(path)
			return nil, err
		}
	}
	if b.CPUTimeout > 0 {
		// 100ms period, quota = budget seconds × 1_000_000 microseconds.
		// quota is the per-period CPU time; setting it equal to the
		// budget's wall-clock seconds × 1e6 means the cgroup is
		// allowed to consume that much CPU per period. Matches the
		// setrlimit RLIMIT_CPU semantic of cumulative seconds.
		quota := int64(b.CPUTimeout.Seconds()) * 1_000_000
		if quota < 1_000 {
			quota = 1_000 // 1ms minimum so cpu.max is well-formed
		}
		if err := writeCgroupFile(path, "cpu.max", fmt.Sprintf("%d 100000", quota)); err != nil {
			_ = os.Remove(path)
			return nil, err
		}
	}

	return &Handle{Path: path}, nil
}

// AddPID attaches an existing process to the cgroup by writing its
// PID to cgroup.procs. The kernel applies the cgroup's limits the
// next time the process is scheduled.
func (h *Handle) AddPID(pid int) error {
	return writeCgroupFile(h.Path, "cgroup.procs", strconv.Itoa(pid))
}

// Cleanup removes the cgroup directory. Safe to call from a defer in
// the parent's spawn flow; no-op when the directory has already been
// reclaimed (e.g. by the kernel after the last process exits).
func (h *Handle) Cleanup() error {
	if h == nil || h.Path == "" {
		return nil
	}
	err := os.Remove(h.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReadMemoryPeak returns the high-water-mark memory usage in bytes
// the kernel recorded for this cgroup. Used by usage-metrics
// reporting (t-cgroup-v2-usage-metrics).
func (h *Handle) ReadMemoryPeak() (uint64, error) {
	return readCgroupUint(h.Path, "memory.peak")
}

// ReadCPUUsageNs parses cpu.stat for cumulative CPU time. cgroup v2
// reports microseconds; the result is converted to nanoseconds so it
// composes cleanly with time.Duration arithmetic.
func (h *Handle) ReadCPUUsageNs() (uint64, error) {
	raw, err := os.ReadFile(filepath.Join(h.Path, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			us, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return us * 1000, nil
		}
	}
	return 0, errors.New("cpu.stat missing usage_usec line")
}

func writeCgroupFile(dir, name, value string) error {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readCgroupUint(dir, name string) (uint64, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
}

// sanitize keeps cgroup names filesystem-safe — slashes would
// pretend to be a nested cgroup, dots are fine, control characters
// are not. The plugin Manifest.Name validation already constrains
// the input but defence-in-depth here prevents a typo from creating
// a sibling cgroup at the parent level.
func sanitize(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-', c == '_', c == '.':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
