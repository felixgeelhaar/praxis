package federation_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/mcp/federation"
)

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fed.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_EmptyPathSignalsDisabled(t *testing.T) {
	_, err := federation.LoadConfig("")
	if !errors.Is(err, federation.ErrConfigPathEmpty) {
		t.Errorf("err=%v want ErrConfigPathEmpty", err)
	}
}

func TestLoadConfig_ParsesURLAndStdioUpstreams(t *testing.T) {
	path := writeYAML(t, `
upstreams:
  - name: vendor-x
    url: https://mcp.vendor-x.example
    token: secret
    allow: [create_ticket, close_ticket]
  - name: local-tool
    command: [/usr/local/bin/mcp-tool, --port, "0"]
`)
	cfg, err := federation.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Upstreams) != 2 {
		t.Fatalf("upstreams=%d want 2", len(cfg.Upstreams))
	}
	if cfg.Upstreams[0].Name != "vendor-x" || cfg.Upstreams[0].Token != "secret" {
		t.Errorf("vendor-x=%+v", cfg.Upstreams[0])
	}
	if len(cfg.Upstreams[0].Allow) != 2 {
		t.Errorf("allow=%v", cfg.Upstreams[0].Allow)
	}
	if len(cfg.Upstreams[1].Command) != 3 {
		t.Errorf("command=%v", cfg.Upstreams[1].Command)
	}
}

func TestLoadConfig_RejectsBothURLAndCommand(t *testing.T) {
	path := writeYAML(t, `
upstreams:
  - name: dual
    url: https://x.example
    command: [/bin/x]
`)
	if _, err := federation.LoadConfig(path); err == nil {
		t.Error("expected error for both url + command")
	}
}

func TestLoadConfig_RejectsNeitherURLNorCommand(t *testing.T) {
	path := writeYAML(t, `
upstreams:
  - name: empty
`)
	if _, err := federation.LoadConfig(path); err == nil {
		t.Error("expected error for missing url + command")
	}
}

func TestLoadConfig_RejectsDuplicateName(t *testing.T) {
	path := writeYAML(t, `
upstreams:
  - name: a
    url: https://x.example
  - name: a
    url: https://y.example
`)
	if _, err := federation.LoadConfig(path); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestLoadConfig_RejectsEmptyName(t *testing.T) {
	path := writeYAML(t, `
upstreams:
  - url: https://x.example
`)
	if _, err := federation.LoadConfig(path); err == nil {
		t.Error("expected empty-name error")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	if _, err := federation.LoadConfig("/nonexistent/praxis-federation"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadConfig_MalformedYAML(t *testing.T) {
	path := writeYAML(t, "upstreams:\n - { bad")
	if _, err := federation.LoadConfig(path); err == nil {
		t.Error("expected parse error")
	}
}
