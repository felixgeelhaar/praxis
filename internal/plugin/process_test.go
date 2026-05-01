package plugin_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/plugin"
	"github.com/felixgeelhaar/praxis/internal/plugin/ipc"
)

// fakeChild simulates the praxis-pluginhost binary entirely in-process:
// it owns a goroutine that reads frames from the parent's "stdin"
// (a pipe pair) and writes responses to the parent's "stdout".
type fakeChild struct {
	manifest ipc.ManifestResult
	caps     []ipc.CapabilityDescriptor
	exec     func(capName string, payload map[string]any) (map[string]any, error)

	wg sync.WaitGroup
}

func (f *fakeChild) serve(in io.Reader, out io.Writer, done chan struct{}) {
	defer close(done)
	codec := ipc.NewCodec(in, out)
	for {
		fr, err := codec.Recv()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go func(fr ipc.Frame) {
			defer f.wg.Done()
			resp := ipc.Frame{ID: fr.ID}
			switch fr.Method {
			case ipc.MethodManifest:
				resp.Result, _ = ipc.EncodeResult(f.manifest)
			case ipc.MethodCapabilities:
				resp.Result, _ = ipc.EncodeResult(ipc.CapabilitiesResult{Capabilities: f.caps})
			case ipc.MethodExecute, ipc.MethodSimulate:
				var ep ipc.ExecuteParams
				_ = ipc.DecodeParams(fr.Params, &ep)
				out, err := f.exec(ep.Capability, ep.Payload)
				if err != nil {
					resp.Error = err.Error()
				} else {
					resp.Result, _ = ipc.EncodeResult(ipc.ExecuteResult{Output: out})
				}
			default:
				resp.Error = "unknown method"
			}
			_ = codec.Send(resp)
		}(fr)
	}
}

func newProcessOpener(t *testing.T, child *fakeChild) *plugin.ProcessOpener {
	t.Helper()
	return openerFromChild(child)
}

// newProcessOpenerForBench is the *testing.B variant; benches and
// tests share the same pipe-paired SpawnFn through openerFromChild.
func newProcessOpenerForBench(_ testing.TB, child *fakeChild) *plugin.ProcessOpener {
	return openerFromChild(child)
}

func openerFromChild(child *fakeChild) *plugin.ProcessOpener {
	return &plugin.ProcessOpener{
		SpawnFn: func(_ context.Context, _ string) (io.WriteCloser, io.ReadCloser, func() error, error) {
			parentToChild := newPipe()
			childToParent := newPipe()
			done := make(chan struct{})
			go child.serve(parentToChild.r, childToParent.w, done)
			kill := func() error {
				_ = parentToChild.w.Close()
				<-done
				return nil
			}
			return parentToChild.w, childToParent.r, kill, nil
		},
	}
}

func TestProcessOpener_HandshakeAndExecute(t *testing.T) {
	child := &fakeChild{
		manifest: ipc.ManifestResult{Name: "remote", Version: "2.0.0"},
		caps: []ipc.CapabilityDescriptor{
			{Name: "remote_cap", Description: "test", Simulatable: true, Idempotent: true},
		},
		exec: func(capName string, payload map[string]any) (map[string]any, error) {
			return map[string]any{"echoed": payload["msg"], "by": capName}, nil
		},
	}
	op := newProcessOpener(t, child)
	p, err := op.Open("/tmp/fake.so")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Manifest().Name != "remote" {
		t.Errorf("Manifest=%+v", p.Manifest())
	}

	regs, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(regs) != 1 || regs[0].Capability.Name != "remote_cap" {
		t.Fatalf("Capabilities=%+v", regs)
	}

	out, err := regs[0].Handler.Execute(context.Background(), map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["echoed"] != "hi" || out["by"] != "remote_cap" {
		t.Errorf("out=%+v", out)
	}

	out, err = regs[0].Handler.Simulate(context.Background(), map[string]any{"msg": "preview"})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["echoed"] != "preview" {
		t.Errorf("Simulate out=%+v", out)
	}

	if cl, ok := p.(interface{ Close() error }); ok {
		_ = cl.Close()
	}
}

func TestProcessOpener_HandlerErrorSurfaces(t *testing.T) {
	child := &fakeChild{
		manifest: ipc.ManifestResult{Name: "x", Version: "1"},
		caps:     []ipc.CapabilityDescriptor{{Name: "broken"}},
		exec: func(_ string, _ map[string]any) (map[string]any, error) {
			return nil, errors.New("vendor 503")
		},
	}
	op := newProcessOpener(t, child)
	p, err := op.Open("/tmp/x.so")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	regs, _ := p.Capabilities(context.Background())
	_, err = regs[0].Handler.Execute(context.Background(), nil)
	if err == nil || err.Error() != "vendor 503" {
		t.Errorf("err=%v want vendor 503", err)
	}
}

func TestProcessOpener_ConcurrentCallsCorrelate(t *testing.T) {
	child := &fakeChild{
		manifest: ipc.ManifestResult{Name: "c", Version: "1"},
		caps:     []ipc.CapabilityDescriptor{{Name: "echo"}},
		exec: func(_ string, payload map[string]any) (map[string]any, error) {
			return map[string]any{"id": payload["id"]}, nil
		},
	}
	op := newProcessOpener(t, child)
	p, _ := op.Open("/tmp/c.so")
	regs, _ := p.Capabilities(context.Background())
	h := regs[0].Handler

	const N = 30
	results := make([]map[string]any, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = h.Execute(context.Background(), map[string]any{"id": i})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
			continue
		}
		// JSON round-trips int as float64; compare via float equality.
		if got, _ := results[i]["id"].(float64); int(got) != i {
			t.Errorf("call %d: result=%v", i, results[i])
		}
	}
}

// pipe is a simple in-process pipe pair adapter. io.Pipe's Reader and
// Writer don't satisfy io.ReadCloser / io.WriteCloser directly, so
// the test wraps them with explicit Close passthroughs.
type pipe struct {
	r io.ReadCloser
	w io.WriteCloser
}

func newPipe() *pipe {
	r, w := io.Pipe()
	return &pipe{r: r, w: w}
}

func TestBudgetEnv_PassthroughWhenZero(t *testing.T) {
	in := []string{"PATH=/usr/bin", "LANG=C"}
	out := plugin.BudgetEnvForTest(in, plugin.ResourceBudget{})
	if len(out) != 2 || out[0] != "PATH=/usr/bin" || out[1] != "LANG=C" {
		t.Errorf("out=%v", out)
	}
}

func TestBudgetEnv_AppendsCPUAndMem(t *testing.T) {
	in := []string{"PATH=/usr/bin"}
	out := plugin.BudgetEnvForTest(in, plugin.ResourceBudget{
		CPUTimeout:     30 * time.Second,
		MaxMemoryBytes: 100 << 20,
	})
	wantCPU := "PRAXIS_PLUGIN_BUDGET_CPU_SEC=30"
	wantMem := "PRAXIS_PLUGIN_BUDGET_MEM_BYTES=104857600"
	var sawCPU, sawMem bool
	for _, e := range out {
		if e == wantCPU {
			sawCPU = true
		}
		if e == wantMem {
			sawMem = true
		}
	}
	if !sawCPU {
		t.Errorf("missing %s in %v", wantCPU, out)
	}
	if !sawMem {
		t.Errorf("missing %s in %v", wantMem, out)
	}
}
