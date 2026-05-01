//go:build !linux

package cgroup

import (
	"errors"
	"time"
)

// Budget mirrors the linux variant for cross-platform compilation.
// The non-linux Prepare always errors so callers fall back to
// setrlimit without checking GOOS at the call site.
type Budget struct {
	CPUTimeout     time.Duration
	MaxMemoryBytes uint64
}

// Handle is a stub on non-linux platforms.
type Handle struct{ Path string }

// ErrUnsupported signals that the current platform has no cgroup v2
// implementation. The caller should fall through to setrlimit.
var ErrUnsupported = errors.New("cgroup v2 not supported on this platform")

// Prepare always returns ErrUnsupported.
func Prepare(_ string, _ string, _ Budget) (*Handle, error) { return nil, ErrUnsupported }

// AddPID is a no-op when the platform has no cgroup support.
func (h *Handle) AddPID(_ int) error { return ErrUnsupported }

// Cleanup is a no-op.
func (h *Handle) Cleanup() error { return nil }

// ReadMemoryPeak / ReadCPUUsageNs return ErrUnsupported.
func (h *Handle) ReadMemoryPeak() (uint64, error) { return 0, ErrUnsupported }
func (h *Handle) ReadCPUUsageNs() (uint64, error) { return 0, ErrUnsupported }
