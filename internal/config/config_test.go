package config_test

import (
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "")
	t.Setenv("PRAXIS_DB_CONN", "")
	t.Setenv("PRAXIS_HTTP_PORT", "")
	t.Setenv("PRAXIS_POLICY_MODE", "")

	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DBType != "memory" {
		t.Errorf("DBType=%s want memory", c.DBType)
	}
	if c.HTTPPort != 8080 {
		t.Errorf("HTTPPort=%d want 8080", c.HTTPPort)
	}
	if c.PolicyMode != "allow" {
		t.Errorf("PolicyMode=%s want allow", c.PolicyMode)
	}
	if c.HandlerTimeout != 30*time.Second {
		t.Errorf("HandlerTimeout=%s want 30s", c.HandlerTimeout)
	}
}

func TestLoad_PostgresRequiresConn(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "postgres")
	t.Setenv("PRAXIS_DB_CONN", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for postgres without conn")
	}
}

func TestLoad_UnknownBackend(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "mongodb")
	t.Setenv("PRAXIS_DB_CONN", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestLoad_PluginDirDefaultsEmpty(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_DIR", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PluginDir != "" {
		t.Errorf("PluginDir=%q want empty", c.PluginDir)
	}
}

func TestLoad_AuditRetention_DefaultsEmpty(t *testing.T) {
	t.Setenv("PRAXIS_AUDIT_RETENTION", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.AuditRetention) != 0 {
		t.Errorf("AuditRetention=%v want empty", c.AuditRetention)
	}
}

func TestLoad_AuditRetention_ParsesPairs(t *testing.T) {
	t.Setenv("PRAXIS_AUDIT_RETENTION", "*=720h,org-x=2160h,org-y=0s")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AuditRetention[""] != 720*time.Hour {
		t.Errorf("default=%v want 720h", c.AuditRetention[""])
	}
	if c.AuditRetention["org-x"] != 2160*time.Hour {
		t.Errorf("org-x=%v want 2160h", c.AuditRetention["org-x"])
	}
	if _, ok := c.AuditRetention["org-y"]; !ok {
		t.Errorf("org-y missing from %v", c.AuditRetention)
	}
}

func TestLoad_AuditRetention_DropsMalformed(t *testing.T) {
	t.Setenv("PRAXIS_AUDIT_RETENTION", "no-equals,key=not-a-duration,*=12h")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.AuditRetention) != 1 || c.AuditRetention[""] != 12*time.Hour {
		t.Errorf("AuditRetention=%v want only default=12h", c.AuditRetention)
	}
}

func TestLoad_AuditRetentionInterval_Default(t *testing.T) {
	t.Setenv("PRAXIS_AUDIT_RETENTION_INTERVAL", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AuditRetentionInterval != time.Hour {
		t.Errorf("Interval=%v want 1h", c.AuditRetentionInterval)
	}
}

func TestLoad_AuditRetentionInitialDelay_Default(t *testing.T) {
	t.Setenv("PRAXIS_AUDIT_RETENTION_INITIAL_DELAY", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AuditRetentionInitialDelay != 5*time.Minute {
		t.Errorf("InitialDelay=%v want 5m", c.AuditRetentionInitialDelay)
	}
}

func TestLoad_AuditRetentionInterval_Override(t *testing.T) {
	t.Setenv("PRAXIS_AUDIT_RETENTION_INTERVAL", "15m")
	t.Setenv("PRAXIS_AUDIT_RETENTION_INITIAL_DELAY", "30s")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AuditRetentionInterval != 15*time.Minute {
		t.Errorf("Interval=%v", c.AuditRetentionInterval)
	}
	if c.AuditRetentionInitialDelay != 30*time.Second {
		t.Errorf("InitialDelay=%v", c.AuditRetentionInitialDelay)
	}
}

func TestLoad_PluginOutOfProcess_DefaultsOff(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_OUT_OF_PROCESS", "")
	t.Setenv("PRAXIS_PLUGINHOST_BINARY", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PluginOutOfProcess {
		t.Error("PluginOutOfProcess defaults true; want false")
	}
}

func TestLoad_PluginOutOfProcess_RequiresHostBinary(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_OUT_OF_PROCESS", "1")
	t.Setenv("PRAXIS_PLUGINHOST_BINARY", "")
	if _, err := config.Load(); err == nil {
		t.Error("OOP without binary path must error")
	}
}

func TestLoad_PluginOutOfProcess_AcceptsHostBinary(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_OUT_OF_PROCESS", "1")
	t.Setenv("PRAXIS_PLUGINHOST_BINARY", "/usr/local/bin/praxis-pluginhost")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.PluginOutOfProcess || c.PluginHostBinary != "/usr/local/bin/praxis-pluginhost" {
		t.Errorf("config=%+v", c)
	}
}

func TestLoad_TLS_DefaultsEmpty(t *testing.T) {
	t.Setenv("PRAXIS_TLS_CERT_FILE", "")
	t.Setenv("PRAXIS_TLS_KEY_FILE", "")
	t.Setenv("PRAXIS_MTLS_CLIENT_CA_FILE", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TLSCertFile != "" || c.TLSKeyFile != "" || c.MTLSClientCAFile != "" {
		t.Errorf("TLS fields=%+v want empty", c)
	}
}

func TestLoad_TLS_CertWithoutKeyRejected(t *testing.T) {
	t.Setenv("PRAXIS_TLS_CERT_FILE", "/etc/praxis/tls.crt")
	t.Setenv("PRAXIS_TLS_KEY_FILE", "")
	t.Setenv("PRAXIS_MTLS_CLIENT_CA_FILE", "")
	if _, err := config.Load(); err == nil {
		t.Error("cert without key must error")
	}
}

func TestLoad_TLS_KeyWithoutCertRejected(t *testing.T) {
	t.Setenv("PRAXIS_TLS_CERT_FILE", "")
	t.Setenv("PRAXIS_TLS_KEY_FILE", "/etc/praxis/tls.key")
	t.Setenv("PRAXIS_MTLS_CLIENT_CA_FILE", "")
	if _, err := config.Load(); err == nil {
		t.Error("key without cert must error")
	}
}

func TestLoad_MTLS_RequiresTLS(t *testing.T) {
	t.Setenv("PRAXIS_TLS_CERT_FILE", "")
	t.Setenv("PRAXIS_TLS_KEY_FILE", "")
	t.Setenv("PRAXIS_MTLS_CLIENT_CA_FILE", "/etc/praxis/clients.pem")
	if _, err := config.Load(); err == nil {
		t.Error("mTLS without TLS must error")
	}
}

func TestLoad_TLS_FullConfigAccepted(t *testing.T) {
	t.Setenv("PRAXIS_TLS_CERT_FILE", "/etc/praxis/tls.crt")
	t.Setenv("PRAXIS_TLS_KEY_FILE", "/etc/praxis/tls.key")
	t.Setenv("PRAXIS_MTLS_CLIENT_CA_FILE", "/etc/praxis/clients.pem")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TLSCertFile != "/etc/praxis/tls.crt" {
		t.Errorf("CertFile=%s", c.TLSCertFile)
	}
	if c.MTLSClientCAFile != "/etc/praxis/clients.pem" {
		t.Errorf("CAFile=%s", c.MTLSClientCAFile)
	}
}

func TestLoad_OTLP_Defaults(t *testing.T) {
	t.Setenv("PRAXIS_OTLP_ENDPOINT", "")
	t.Setenv("PRAXIS_OTLP_PROTOCOL", "")
	t.Setenv("PRAXIS_OTLP_INSECURE", "")
	t.Setenv("PRAXIS_TRACE_SAMPLE", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OTLPEndpoint != "" {
		t.Errorf("OTLPEndpoint=%q want empty", c.OTLPEndpoint)
	}
	if c.OTLPProtocol != "grpc" {
		t.Errorf("OTLPProtocol=%q want grpc", c.OTLPProtocol)
	}
	if c.OTLPInsecure {
		t.Error("OTLPInsecure should default false")
	}
	if c.TraceSample != 1.0 {
		t.Errorf("TraceSample=%v want 1.0", c.TraceSample)
	}
}

func TestLoad_OTLP_Overrides(t *testing.T) {
	t.Setenv("PRAXIS_OTLP_ENDPOINT", "otel-collector:4317")
	t.Setenv("PRAXIS_OTLP_PROTOCOL", "http")
	t.Setenv("PRAXIS_OTLP_INSECURE", "true")
	t.Setenv("PRAXIS_TRACE_SAMPLE", "0.05")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.OTLPEndpoint != "otel-collector:4317" {
		t.Errorf("Endpoint=%q", c.OTLPEndpoint)
	}
	if c.OTLPProtocol != "http" {
		t.Errorf("Protocol=%q", c.OTLPProtocol)
	}
	if !c.OTLPInsecure {
		t.Error("Insecure should be true")
	}
	if c.TraceSample != 0.05 {
		t.Errorf("Sample=%v want 0.05", c.TraceSample)
	}
}

func TestLoad_PluginAutoreload_DefaultsOn(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_AUTORELOAD", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.PluginAutoreload {
		t.Error("PluginAutoreload default must be true")
	}
}

func TestLoad_PluginAutoreload_ExplicitOff(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "off"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("PRAXIS_PLUGIN_AUTORELOAD", v)
			c, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.PluginAutoreload {
				t.Errorf("PluginAutoreload=%s should be false", v)
			}
		})
	}
}

func TestLoad_PluginStrict_Default(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_STRICT", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.PluginStrict {
		t.Error("PluginStrict default must be true (fail-stop on plugin load failure)")
	}
}

func TestLoad_PluginStrict_TruthyValues(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("PRAXIS_PLUGIN_STRICT", v)
			c, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !c.PluginStrict {
				t.Errorf("PluginStrict=%s should be truthy", v)
			}
		})
	}
}

func TestLoad_PluginStrict_FalsyValues(t *testing.T) {
	// "garbage" hits parseBoolDefault's fallback to default (true), so omit it
	// from the falsy set — only well-formed boolean negatives flip strict off.
	for _, v := range []string{"0", "false", "no", "off"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("PRAXIS_PLUGIN_STRICT", v)
			c, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.PluginStrict {
				t.Errorf("PluginStrict=%s should be falsy", v)
			}
		})
	}
}

func TestLoad_PluginTrustedKeys_DefaultsNil(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_TRUSTED_KEYS", "")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PluginTrustedKeys != nil {
		t.Errorf("PluginTrustedKeys=%v want nil", c.PluginTrustedKeys)
	}
}

func TestLoad_PluginTrustedKeys_ParsesList(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_TRUSTED_KEYS", "/etc/praxis/k1.pub, /etc/praxis/k2.pub")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.PluginTrustedKeys) != 2 ||
		c.PluginTrustedKeys[0] != "/etc/praxis/k1.pub" ||
		c.PluginTrustedKeys[1] != "/etc/praxis/k2.pub" {
		t.Errorf("PluginTrustedKeys=%v", c.PluginTrustedKeys)
	}
}

func TestLoad_PluginFulcioRoots_RequiresSubjects(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_FULCIO_ROOTS", "/etc/praxis/root.pem")
	t.Setenv("PRAXIS_PLUGIN_FULCIO_ISSUER", "https://token.actions.githubusercontent.com")
	if _, err := config.Load(); err == nil {
		t.Errorf("expected error when fulcio roots set without subjects")
	}
}

func TestLoad_PluginFulcioRoots_RequiresIssuer(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_FULCIO_ROOTS", "/etc/praxis/root.pem")
	t.Setenv("PRAXIS_PLUGIN_FULCIO_SUBJECTS", "https://github.com/me/*")
	if _, err := config.Load(); err == nil {
		t.Errorf("expected error when fulcio roots set without issuer")
	}
}

func TestLoad_PluginFulcioRoots_FullPolicy(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_FULCIO_ROOTS", "/etc/praxis/root.pem")
	t.Setenv("PRAXIS_PLUGIN_FULCIO_SUBJECTS", "https://github.com/me/*,https://github.com/team/*")
	t.Setenv("PRAXIS_PLUGIN_FULCIO_ISSUER", "https://token.actions.githubusercontent.com")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.PluginFulcioRoots) != 1 || len(c.PluginFulcioSubjects) != 2 {
		t.Errorf("roots=%v subjects=%v", c.PluginFulcioRoots, c.PluginFulcioSubjects)
	}
	if c.PluginFulcioIssuer != "https://token.actions.githubusercontent.com" {
		t.Errorf("issuer=%q", c.PluginFulcioIssuer)
	}
}

func TestLoad_PluginDirOverride(t *testing.T) {
	t.Setenv("PRAXIS_PLUGIN_DIR", "/opt/praxis/plugins")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PluginDir != "/opt/praxis/plugins" {
		t.Errorf("PluginDir=%q", c.PluginDir)
	}
}

func TestLoad_OverridesViaEnv(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "sqlite")
	t.Setenv("PRAXIS_DB_CONN", "praxis.db")
	t.Setenv("PRAXIS_HTTP_PORT", "9999")
	t.Setenv("PRAXIS_POLICY_MODE", "deny")
	t.Setenv("PRAXIS_HANDLER_TIMEOUT", "5s")

	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DBType != "sqlite" {
		t.Errorf("DBType=%s want sqlite", c.DBType)
	}
	if c.DBConn != "praxis.db" {
		t.Errorf("DBConn=%s want praxis.db", c.DBConn)
	}
	if c.HTTPPort != 9999 {
		t.Errorf("HTTPPort=%d want 9999", c.HTTPPort)
	}
	if c.PolicyMode != "deny" {
		t.Errorf("PolicyMode=%s want deny", c.PolicyMode)
	}
	if c.HandlerTimeout != 5*time.Second {
		t.Errorf("HandlerTimeout=%s want 5s", c.HandlerTimeout)
	}
}
