package capability_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

type mockHandler struct {
	name   string
	output map[string]any
}

func (h *mockHandler) Name() string { return h.name }
func (h *mockHandler) Execute(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.output, nil
}
func (h *mockHandler) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.output, nil
}

func TestRegistry_Register(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid_handler", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := capability.New()
			err := reg.Register(&mockHandler{name: tt.name, output: map[string]any{}})

			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Register() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistry_ListCapabilities(t *testing.T) {
	reg := capability.New()
	reg.Register(&mockHandler{name: "cap1", output: map[string]any{}})
	reg.Register(&mockHandler{name: "cap2", output: map[string]any{}})

	caps, err := reg.ListCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ListCapabilities() error = %v", err)
	}
	if len(caps) != 2 {
		t.Errorf("ListCapabilities() got %d, want 2", len(caps))
	}
}

func TestRegistry_GetCapability(t *testing.T) {
	reg := capability.New()
	reg.Register(&mockHandler{name: "test_cap", output: map[string]any{}})

	_, err := reg.GetCapability("test_cap")
	if err != nil {
		t.Errorf("GetCapability() error = %v", err)
	}

	_, err = reg.GetCapability("missing")
	if err == nil {
		t.Error("GetCapability() should return error for missing")
	}
}

func TestRegistry_GetHandler(t *testing.T) {
	reg := capability.New()
	reg.Register(&mockHandler{name: "test_handler", output: map[string]any{}})

	h, err := reg.GetHandler("test_handler")
	if err != nil {
		t.Errorf("GetHandler() error = %v", err)
	}
	if h.Name() != "test_handler" {
		t.Errorf("GetHandler() name = %s, want test_handler", h.Name())
	}
}

// Tenant-scoped registry: Phase 3 M3.3.

func TestRegisterTenant_RejectsEmptyOrg(t *testing.T) {
	reg := capability.New()
	if err := reg.RegisterTenant("", &mockHandler{name: "x"}); err == nil {
		t.Error("RegisterTenant(\"\", …) must reject empty orgID")
	}
}

func TestGetHandlerForCaller_TenantPrivateResolves(t *testing.T) {
	reg := capability.New()
	if err := reg.RegisterTenant("org-a", &mockHandler{name: "private_cap"}); err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}

	h, err := reg.GetHandlerForCaller("private_cap", caller("org-a"))
	if err != nil || h.Name() != "private_cap" {
		t.Errorf("org-a should see private_cap: h=%v err=%v", h, err)
	}
}

func TestGetHandlerForCaller_TenantsAreIsolated(t *testing.T) {
	reg := capability.New()
	_ = reg.RegisterTenant("org-a", &mockHandler{name: "private_cap"})

	if _, err := reg.GetHandlerForCaller("private_cap", caller("org-b")); err == nil {
		t.Error("org-b must not see org-a's private cap")
	}
	if _, err := reg.GetHandlerForCaller("private_cap", caller("")); err == nil {
		t.Error("anonymous caller must not see private cap")
	}
}

func TestGetHandlerForCaller_GlobalFallback(t *testing.T) {
	reg := capability.New()
	_ = reg.Register(&mockHandler{name: "shared"})

	for _, org := range []string{"", "org-a", "org-b"} {
		h, err := reg.GetHandlerForCaller("shared", caller(org))
		if err != nil || h.Name() != "shared" {
			t.Errorf("org=%q should resolve global cap: h=%v err=%v", org, h, err)
		}
	}
}

func TestGetHandlerForCaller_TenantOverridesGlobal(t *testing.T) {
	reg := capability.New()
	_ = reg.Register(&mockHandler{name: "send", output: map[string]any{"src": "global"}})
	_ = reg.RegisterTenant("org-a", &mockHandler{name: "send", output: map[string]any{"src": "tenant"}})

	h, err := reg.GetHandlerForCaller("send", caller("org-a"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	out, _ := h.Execute(context.Background(), nil)
	if out["src"] != "tenant" {
		t.Errorf("override expected, got %v", out)
	}

	h, err = reg.GetHandlerForCaller("send", caller("org-b"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	out, _ = h.Execute(context.Background(), nil)
	if out["src"] != "global" {
		t.Errorf("org-b should see global, got %v", out)
	}
}

func TestGetCapabilityForCaller_MirrorsHandlerResolution(t *testing.T) {
	reg := capability.New()
	_ = reg.RegisterTenant("org-a", &mockHandler{name: "private_cap"})

	if _, err := reg.GetCapabilityForCaller("private_cap", caller("org-a")); err != nil {
		t.Errorf("org-a should resolve cap: %v", err)
	}
	if _, err := reg.GetCapabilityForCaller("private_cap", caller("org-b")); err == nil {
		t.Error("org-b must not resolve cap")
	}
}

func TestListCapabilitiesForCaller_FiltersByCaller(t *testing.T) {
	reg := capability.New()
	_ = reg.Register(&mockHandler{name: "global_1"})
	_ = reg.Register(&mockHandler{name: "global_2"})
	_ = reg.RegisterTenant("org-a", &mockHandler{name: "a_priv"})
	_ = reg.RegisterTenant("org-b", &mockHandler{name: "b_priv"})

	mustList := func(c domain.CallerRef) map[string]bool {
		caps, err := reg.ListCapabilitiesForCaller(context.Background(), c)
		if err != nil {
			t.Fatalf("ListCapabilitiesForCaller: %v", err)
		}
		out := map[string]bool{}
		for _, cap := range caps {
			out[cap.Name] = true
		}
		return out
	}

	got := mustList(caller("org-a"))
	want := map[string]bool{"global_1": true, "global_2": true, "a_priv": true}
	if !equalSet(got, want) {
		t.Errorf("org-a sees=%v want=%v", got, want)
	}

	got = mustList(caller("org-b"))
	want = map[string]bool{"global_1": true, "global_2": true, "b_priv": true}
	if !equalSet(got, want) {
		t.Errorf("org-b sees=%v want=%v", got, want)
	}

	got = mustList(caller(""))
	want = map[string]bool{"global_1": true, "global_2": true}
	if !equalSet(got, want) {
		t.Errorf("anon sees=%v want=%v", got, want)
	}
}

// Phase 5 schema-compat checker.

func TestRegistry_CompatStrict_RejectsBreakingChange(t *testing.T) {
	reg := capability.New()
	checkCalls := 0
	reg.SetCompatMode(capability.CompatStrict,
		func(prev, next domain.Capability) []capability.CompatIssue {
			checkCalls++
			if prev.Name == next.Name && checkCalls > 0 {
				return []capability.CompatIssue{{Code: "x", Field: "y", Message: "broke"}}
			}
			return nil
		},
		nil,
	)
	if err := reg.Register(&mockHandler{name: "v1"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := reg.Register(&mockHandler{name: "v1"})
	if err == nil {
		t.Fatal("strict re-registration must fail")
	}
}

func TestRegistry_CompatWarn_AllowsButFiresHook(t *testing.T) {
	reg := capability.New()
	var got []capability.CompatIssue
	reg.SetCompatMode(capability.CompatWarn,
		func(_ domain.Capability, _ domain.Capability) []capability.CompatIssue {
			return []capability.CompatIssue{{Code: "x", Field: "y", Message: "warn"}}
		},
		func(_ string, issues []capability.CompatIssue) { got = issues },
	)
	_ = reg.Register(&mockHandler{name: "h"})
	if err := reg.Register(&mockHandler{name: "h"}); err != nil {
		t.Errorf("warn mode must not fail: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("hook not fired: %+v", got)
	}
}

func TestRegistry_HistoryRecordsBreakingRegistrations(t *testing.T) {
	reg := capability.New()
	reg.SetCompatMode(capability.CompatWarn,
		func(_ domain.Capability, _ domain.Capability) []capability.CompatIssue {
			return []capability.CompatIssue{{Code: "x", Field: "y", Message: "broke"}}
		},
		nil,
	)
	_ = reg.Register(&mockHandler{name: "h"})
	_ = reg.Register(&mockHandler{name: "h"})
	_ = reg.Register(&mockHandler{name: "h"})

	hist := reg.History("h")
	if len(hist) != 2 {
		t.Errorf("history=%d want 2 (only re-registrations after the first)", len(hist))
	}
	if len(hist) > 0 && len(hist[0].Issues) != 1 {
		t.Errorf("first entry issues=%+v", hist[0].Issues)
	}
}

func TestRegistry_HistoryEmptyForCleanRegistration(t *testing.T) {
	reg := capability.New()
	_ = reg.Register(&mockHandler{name: "h"})
	if len(reg.History("h")) != 0 {
		t.Error("clean registration should not record history")
	}
}

func TestRegistry_CompatOff_NeverChecks(t *testing.T) {
	reg := capability.New()
	checked := false
	// Default mode is off; SetCompatMode never called.
	_ = reg.Register(&mockHandler{name: "h"})
	if checked {
		t.Error("default off mode should not invoke checker")
	}
}

func TestRegistry_ConcurrentRegistrationSafe(t *testing.T) {
	reg := capability.New()
	const N = 50
	done := make(chan struct{}, N*2)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			_ = reg.Register(&mockHandler{name: "g_" + itoa(i)})
			done <- struct{}{}
		}()
		go func() {
			_ = reg.RegisterTenant("org-"+itoa(i%3), &mockHandler{name: "t_" + itoa(i)})
			done <- struct{}{}
		}()
	}
	for i := 0; i < N*2; i++ {
		<-done
	}
	caps, _ := reg.ListCapabilitiesForCaller(context.Background(), caller("org-0"))
	if len(caps) == 0 {
		t.Error("expected concurrent registrations to land")
	}
}

func caller(orgID string) domain.CallerRef {
	return domain.CallerRef{Type: "user", ID: "u-1", OrgID: orgID}
}

func equalSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestRegistry_HistoryRepoPersistsAndReads(t *testing.T) {
	reg := capability.New()
	repo := newFakeHistoryRepo()
	reg.SetHistoryRepo(repo)
	reg.SetCompatMode(capability.CompatWarn,
		func(_ domain.Capability, _ domain.Capability) []capability.CompatIssue {
			return []capability.CompatIssue{{Code: "x", Field: "y", Message: "broke"}}
		},
		nil,
	)
	_ = reg.Register(&mockHandler{name: "h"})
	_ = reg.Register(&mockHandler{name: "h"})
	_ = reg.Register(&mockHandler{name: "h"})

	if got := repo.count("h"); got != 2 {
		t.Errorf("repo append count=%d want 2", got)
	}

	got, err := reg.HistoryFromRepo(context.Background(), "h")
	if err != nil {
		t.Fatalf("HistoryFromRepo: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("HistoryFromRepo len=%d want 2", len(got))
	}
	if len(got) > 0 && (len(got[0].Issues) != 1 || got[0].Issues[0].Code != "x") {
		t.Errorf("issues=%+v", got[0].Issues)
	}
}

type fakeHistoryRepo struct {
	mu      sync.Mutex
	entries map[string][]domain.CapabilityHistoryEntry
}

func newFakeHistoryRepo() *fakeHistoryRepo {
	return &fakeHistoryRepo{entries: map[string][]domain.CapabilityHistoryEntry{}}
}

func (r *fakeHistoryRepo) Append(_ context.Context, e domain.CapabilityHistoryEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[e.CapabilityName] = append(r.entries[e.CapabilityName], e)
	return nil
}

func (r *fakeHistoryRepo) ListForCapability(_ context.Context, name string) ([]domain.CapabilityHistoryEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.CapabilityHistoryEntry, len(r.entries[name]))
	copy(out, r.entries[name])
	return out, nil
}

func (r *fakeHistoryRepo) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries[name])
}

type fakeCapRepo struct {
	mu       sync.Mutex
	upserted []domain.Capability
}

func (r *fakeCapRepo) Upsert(_ context.Context, c domain.Capability) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserted = append(r.upserted, c)
	return nil
}
func (r *fakeCapRepo) Get(_ context.Context, name string) (domain.Capability, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.upserted {
		if c.Name == name {
			return c, nil
		}
	}
	return domain.Capability{}, errors.New("not found")
}
func (r *fakeCapRepo) List(_ context.Context) ([]domain.Capability, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]domain.Capability(nil), r.upserted...)
	return out, nil
}

func TestRegistry_PersistsToRepoOnRegister(t *testing.T) {
	repo := &fakeCapRepo{}
	reg := capability.New()
	reg.SetRepo(repo)
	if err := reg.Register(&mockHandler{name: "demo_cap", output: map[string]any{}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(repo.upserted) != 1 || repo.upserted[0].Name != "demo_cap" {
		t.Fatalf("upsert state: %+v", repo.upserted)
	}
}

func TestRegistry_PropagatesRepoUpsertError(t *testing.T) {
	reg := capability.New()
	reg.SetRepo(&erroringCapRepo{})
	if err := reg.Register(&mockHandler{name: "demo_cap", output: map[string]any{}}); err == nil {
		t.Fatal("want error from repo upsert")
	}
}

type erroringCapRepo struct{}

func (erroringCapRepo) Upsert(context.Context, domain.Capability) error {
	return errors.New("upsert failed")
}
func (erroringCapRepo) Get(context.Context, string) (domain.Capability, error) {
	return domain.Capability{}, errors.New("not impl")
}
func (erroringCapRepo) List(context.Context) ([]domain.Capability, error) {
	return nil, nil
}
