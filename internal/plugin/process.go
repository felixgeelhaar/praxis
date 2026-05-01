package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/plugin/ipc"
)

// Env-var names mirroring the contract on the host (cmd/praxis-pluginhost).
// Defining the constants here as well lets the parent set them without
// importing the cmd package.
const (
	envBudgetCPUSeconds = "PRAXIS_PLUGIN_BUDGET_CPU_SEC"
	envBudgetMemBytes   = "PRAXIS_PLUGIN_BUDGET_MEM_BYTES"
)

// BudgetEnvForTest exposes budgetEnv to the package's external test
// suite. Production callers should not depend on this.
func BudgetEnvForTest(env []string, budget ResourceBudget) []string {
	return budgetEnv(env, budget)
}

// budgetEnv copies env and appends the budget vars when budget has any
// non-zero field. Empty budgets pass env through unchanged so the
// parent's resolved environment (PATH, locale, etc.) reaches the
// child unmodified.
func budgetEnv(env []string, budget ResourceBudget) []string {
	if budget.CPUTimeout == 0 && budget.MaxMemoryBytes == 0 {
		return env
	}
	out := append([]string(nil), env...)
	if budget.CPUTimeout > 0 {
		out = append(out, fmt.Sprintf("%s=%d", envBudgetCPUSeconds, int64(budget.CPUTimeout.Seconds())))
	}
	if budget.MaxMemoryBytes > 0 {
		out = append(out, fmt.Sprintf("%s=%d", envBudgetMemBytes, budget.MaxMemoryBytes))
	}
	return out
}

// ProcessOpener is an Opener implementation that spawns a
// praxis-pluginhost child process per plugin and proxies the Plugin
// interface over IPC. Phase 4 out-of-process loader.
//
// The Binary field is the absolute path to the praxis-pluginhost
// binary; tests inject their own command for round-tripping the
// protocol against an in-process echo server. Budget, when non-zero,
// is forwarded to the child via PRAXIS_PLUGIN_BUDGET_* env vars and
// the child applies setrlimit at startup.
type ProcessOpener struct {
	Binary string
	Budget ResourceBudget

	// SpawnFn is the test seam: production uses exec.Command, tests
	// supply a custom transport pair without touching the OS.
	SpawnFn func(ctx context.Context, artefactPath string) (io.WriteCloser, io.ReadCloser, func() error, error)
}

// Open spawns a child host for artefactPath and returns a Plugin that
// forwards every call across the IPC boundary. Manifest is fetched
// eagerly so the parent can fail fast on a child that doesn't speak
// the protocol.
func (o *ProcessOpener) Open(artefactPath string) (Plugin, error) {
	ctx := context.Background()
	stdin, stdout, kill, err := o.spawn(ctx, artefactPath)
	if err != nil {
		return nil, fmt.Errorf("spawn pluginhost: %w", err)
	}
	codec := ipc.NewCodec(stdout, stdin)
	p := &processPlugin{
		codec:    codec,
		kill:     kill,
		artefact: artefactPath,
	}
	if err := p.handshake(); err != nil {
		_ = p.Close()
		return nil, err
	}
	return p, nil
}

func (o *ProcessOpener) spawn(ctx context.Context, artefactPath string) (io.WriteCloser, io.ReadCloser, func() error, error) {
	if o.SpawnFn != nil {
		return o.SpawnFn(ctx, artefactPath)
	}
	if o.Binary == "" {
		return nil, nil, nil, errors.New("ProcessOpener: Binary is required")
	}
	cmd := exec.CommandContext(ctx, o.Binary, artefactPath)
	cmd.Env = budgetEnv(os.Environ(), o.Budget)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	kill := func() error {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return cmd.Wait()
	}
	return stdin, stdout, kill, nil
}

// processPlugin proxies the Plugin interface over a Codec to a child
// process. Concurrent Execute calls are serialised through a request
// counter + response correlation so each frame round-trips against
// its origin caller.
type processPlugin struct {
	codec    *ipc.Codec
	kill     func() error
	artefact string

	manifest Manifest

	mu      sync.Mutex
	nextID  atomic.Uint64
	pending map[string]chan ipc.Frame
	once    sync.Once

	// crashed receives the dispatcher's terminal error when the IPC
	// stream closes (child exited, host binary crashed, kernel sent
	// SIGKILL on RLIMIT_*). It is buffered so a fast crash before any
	// observer attaches does not block dispatch.
	crashed chan error
}

func (p *processPlugin) handshake() error {
	p.pending = map[string]chan ipc.Frame{}
	p.crashed = make(chan error, 1)
	go p.dispatch()

	mres, err := p.call(ipc.MethodManifest, ipc.ManifestParams{})
	if err != nil {
		return fmt.Errorf("manifest handshake: %w", err)
	}
	var m ipc.ManifestResult
	if err := ipc.DecodeResult(mres, &m); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	p.manifest = Manifest{
		Name:        m.Name,
		Version:     m.Version,
		Author:      m.Author,
		Description: m.Description,
		Homepage:    m.Homepage,
		License:     m.License,
	}
	return nil
}

// dispatch is the single goroutine that reads frames off the codec
// and routes them to the calling goroutine via the pending map.
// Codec writes are mutex-guarded inside the codec, so concurrent
// callers may call call() without coordinating among themselves.
func (p *processPlugin) dispatch() {
	for {
		f, err := p.codec.Recv()
		if err != nil {
			p.failAllPending(err)
			// Surface the terminal error to the watcher. Buffered
			// channel + non-blocking send so dispatch never hangs
			// when no one is listening.
			select {
			case p.crashed <- err:
			default:
			}
			return
		}
		p.mu.Lock()
		ch, ok := p.pending[f.ID]
		delete(p.pending, f.ID)
		p.mu.Unlock()
		if !ok {
			continue
		}
		ch <- f
	}
}

// Watch implements the Watchable interface so the Manager can be
// notified when the child crashes (or any other event closes the IPC
// stream). The returned channel produces exactly one error and then
// stays open or closes depending on the dispatcher's state. Callers
// should treat any value as the terminal status — recovery happens
// via reload, not retry.
func (p *processPlugin) Watch() <-chan error { return p.crashed }

func (p *processPlugin) failAllPending(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, ch := range p.pending {
		ch <- ipc.Frame{ID: id, Error: err.Error()}
		delete(p.pending, id)
	}
}

func (p *processPlugin) call(method string, params any) ([]byte, error) {
	id := strconv.FormatUint(p.nextID.Add(1), 10)
	raw, err := ipc.EncodeParams(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan ipc.Frame, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	if err := p.codec.Send(ipc.Frame{ID: id, Method: method, Params: raw}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}
	resp := <-ch
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

// ABI implements Plugin. The protocol is versioned implicitly by the
// host binary; the parent always speaks current-Praxis ABI to the
// child it spawned.
func (p *processPlugin) ABI() string { return ABIVersion }

// Manifest implements Plugin.
func (p *processPlugin) Manifest() Manifest { return p.manifest }

// Capabilities implements Plugin. Each remote capability gets its
// own Registration whose Handler proxies Execute/Simulate back over
// the IPC boundary.
func (p *processPlugin) Capabilities(_ context.Context) ([]Registration, error) {
	res, err := p.call(ipc.MethodCapabilities, struct{}{})
	if err != nil {
		return nil, err
	}
	var cr ipc.CapabilitiesResult
	if err := ipc.DecodeResult(res, &cr); err != nil {
		return nil, fmt.Errorf("decode capabilities: %w", err)
	}
	out := make([]Registration, 0, len(cr.Capabilities))
	for _, c := range cr.Capabilities {
		out = append(out, Registration{
			Capability: domain.Capability{
				Name:         c.Name,
				Description:  c.Description,
				InputSchema:  c.InputSchema,
				OutputSchema: c.OutputSchema,
				Permissions:  c.Permissions,
				Simulatable:  c.Simulatable,
				Idempotent:   c.Idempotent,
			},
			Handler: &remoteHandler{plugin: p, name: c.Name},
		})
	}
	return out, nil
}

// Close releases the child process. Idempotent; a second call is a
// no-op so deferred-close from multiple paths cannot crash the
// parent.
func (p *processPlugin) Close() error {
	var err error
	p.once.Do(func() {
		if p.kill != nil {
			err = p.kill()
		}
	})
	return err
}

// remoteHandler is the Handler shape returned to the registry. Each
// invocation sends an IPC frame and blocks on the response.
type remoteHandler struct {
	plugin *processPlugin
	name   string
}

func (h *remoteHandler) Name() string { return h.name }

func (h *remoteHandler) Execute(_ context.Context, payload map[string]any) (map[string]any, error) {
	return h.invoke(ipc.MethodExecute, payload)
}

func (h *remoteHandler) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	return h.invoke(ipc.MethodSimulate, payload)
}

func (h *remoteHandler) invoke(method string, payload map[string]any) (map[string]any, error) {
	res, err := h.plugin.call(method, ipc.ExecuteParams{Capability: h.name, Payload: payload})
	if err != nil {
		return nil, err
	}
	var er ipc.ExecuteResult
	if err := ipc.DecodeResult(res, &er); err != nil {
		return nil, fmt.Errorf("decode execute result: %w", err)
	}
	return er.Output, nil
}

// Compile-time assertion: remoteHandler satisfies capability.Handler.
var _ capability.Handler = (*remoteHandler)(nil)
