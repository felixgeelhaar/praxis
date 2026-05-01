package capability_test

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/capability"
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
