package plugin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/plugin"
)

type fakeHandler struct{ name string }

func (f *fakeHandler) Name() string { return f.name }
func (f *fakeHandler) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}
func (f *fakeHandler) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{"sim": true}, nil
}

type fakePlugin struct {
	abi      string
	manifest plugin.Manifest
	caps     []plugin.Registration
	err      error
}

func (p *fakePlugin) ABI() string               { return p.abi }
func (p *fakePlugin) Manifest() plugin.Manifest { return p.manifest }
func (p *fakePlugin) Capabilities(_ context.Context) ([]plugin.Registration, error) {
	return p.caps, p.err
}

type fakeLoader struct {
	registered []plugin.Registration
	failOn     string
}

func (f *fakeLoader) Register(r plugin.Registration) error {
	if f.failOn != "" && r.Capability.Name == f.failOn {
		return errors.New("loader rejected " + r.Capability.Name)
	}
	f.registered = append(f.registered, r)
	return nil
}

func TestLoad_ABIMatchRegistersCapabilities(t *testing.T) {
	p := &fakePlugin{
		abi:      plugin.ABIVersion,
		manifest: plugin.Manifest{Name: "ext-pagerduty", Version: "1.0.0"},
		caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "pagerduty_create_incident"}, Handler: &fakeHandler{name: "pagerduty_create_incident"}},
			{Capability: domain.Capability{Name: "pagerduty_resolve"}, Handler: &fakeHandler{name: "pagerduty_resolve"}},
		},
	}
	loader := &fakeLoader{}
	if err := plugin.Load(context.Background(), p, loader); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loader.registered) != 2 {
		t.Errorf("registered=%d want 2", len(loader.registered))
	}
}

func TestLoad_ABIMismatchRejected(t *testing.T) {
	p := &fakePlugin{abi: "v0", manifest: plugin.Manifest{Name: "old", Version: "0.1"}}
	loader := &fakeLoader{}
	err := plugin.Load(context.Background(), p, loader)
	if err == nil {
		t.Fatal("expected error")
	}
	var mm *plugin.ABIMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("err not ABIMismatchError: %T", err)
	}
	if mm.Want != plugin.ABIVersion || mm.Got != "v0" {
		t.Errorf("ABIMismatch=%+v", mm)
	}
	if len(loader.registered) != 0 {
		t.Errorf("registered on mismatch: %v", loader.registered)
	}
}

func TestLoad_CapabilitiesError(t *testing.T) {
	p := &fakePlugin{abi: plugin.ABIVersion, err: errors.New("init failed")}
	loader := &fakeLoader{}
	if err := plugin.Load(context.Background(), p, loader); err == nil {
		t.Fatal("expected error")
	}
	if len(loader.registered) != 0 {
		t.Error("registered despite Capabilities error")
	}
}

func TestLoad_LoaderErrorShortCircuits(t *testing.T) {
	p := &fakePlugin{
		abi: plugin.ABIVersion,
		caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "ok-1"}, Handler: &fakeHandler{name: "ok-1"}},
			{Capability: domain.Capability{Name: "boom"}, Handler: &fakeHandler{name: "boom"}},
			{Capability: domain.Capability{Name: "should-not-load"}, Handler: &fakeHandler{name: "should-not-load"}},
		},
	}
	loader := &fakeLoader{failOn: "boom"}
	if err := plugin.Load(context.Background(), p, loader); err == nil {
		t.Fatal("expected loader error to surface")
	}
	if len(loader.registered) != 1 {
		t.Errorf("registered=%d want 1 (short-circuit on boom)", len(loader.registered))
	}
}

// Compile-time interface assertion: domain.Capability + capability.Handler
// stay assignable into a Registration.
var _ = plugin.Registration{
	Capability: domain.Capability{},
	Handler:    capability.Handler(nil),
}

// budgetedFakePlugin extends fakePlugin with Budget() so Load wraps
// every Registration in a Sandboxed handler.
type budgetedFakePlugin struct {
	fakePlugin
	budget plugin.ResourceBudget
}

func (b *budgetedFakePlugin) Budget() plugin.ResourceBudget { return b.budget }

func TestLoad_BudgetedPluginWrapsHandlers(t *testing.T) {
	bp := &budgetedFakePlugin{
		fakePlugin: fakePlugin{
			abi: plugin.ABIVersion,
			caps: []plugin.Registration{
				{Capability: domain.Capability{Name: "x"}, Handler: &fakeHandler{name: "x"}},
			},
		},
		budget: plugin.ResourceBudget{AllowedHosts: []string{"api.example.com"}},
	}
	loader := &fakeLoader{}
	if err := plugin.Load(context.Background(), bp, loader); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loader.registered) != 1 {
		t.Fatalf("registered=%d", len(loader.registered))
	}
	// Handler should be a sandboxed wrapper, not the raw fakeHandler.
	if _, isRaw := loader.registered[0].Handler.(*fakeHandler); isRaw {
		t.Error("expected handler to be wrapped by Sandboxed")
	}
}

func TestLoad_NonBudgetedPluginPassesHandlersUnwrapped(t *testing.T) {
	p := &fakePlugin{
		abi: plugin.ABIVersion,
		caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "y"}, Handler: &fakeHandler{name: "y"}},
		},
	}
	loader := &fakeLoader{}
	if err := plugin.Load(context.Background(), p, loader); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, isRaw := loader.registered[0].Handler.(*fakeHandler); !isRaw {
		t.Error("expected non-budgeted plugin's handler to remain unwrapped")
	}
}
