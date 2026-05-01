// Command praxis is the Phase-1 reference CLI and HTTP server for the
// Praxis execution layer.
//
// Subcommands:
//
//	praxis serve                 — start the HTTP API
//	praxis caps list             — list registered capabilities
//	praxis caps show <name>      — show one capability
//	praxis run <cap> <json>      — execute (or --dry-run) a capability
//	praxis log show <action-id>  — show audit lifecycle for an action
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/config"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	calendarhandler "github.com/felixgeelhaar/praxis/internal/handlers/calendar"
	emailhandler "github.com/felixgeelhaar/praxis/internal/handlers/email"
	githubhandler "github.com/felixgeelhaar/praxis/internal/handlers/github"
	httphandler "github.com/felixgeelhaar/praxis/internal/handlers/http"
	linearhandler "github.com/felixgeelhaar/praxis/internal/handlers/linear"
	slackhandler "github.com/felixgeelhaar/praxis/internal/handlers/slack"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/jobs"
	pmcp "github.com/felixgeelhaar/praxis/internal/mcp"
	"github.com/felixgeelhaar/praxis/internal/outcome"
	"github.com/felixgeelhaar/praxis/internal/plugin"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(runServe())
	case "mcp":
		os.Exit(runMCP())
	case "revert":
		os.Exit(runRevert(os.Args[2:]))
	case "caps":
		os.Exit(runCaps(os.Args[2:]))
	case "run":
		os.Exit(runAction(os.Args[2:]))
	case "log":
		os.Exit(runLog(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Printf("praxis %s (commit %s, built %s)\n", Version, Commit, BuildDate)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Print(`praxis — execution layer of the cognitive stack

Usage:
  praxis serve                       Start HTTP API
  praxis mcp                         Start stdio MCP server
  praxis caps list [--org=<id>] [--team=<id>]
                                     List capabilities (caller-scoped when --org set)
  praxis caps show <name> [--org=<id>] [--team=<id>]
                                     Show one capability
  praxis run <cap> <json> [--dry-run] Execute or simulate a capability
  praxis revert <action-id>          Run the compensating action for a succeeded action
  praxis log show <action-id>        Show audit lifecycle for an action
  praxis version                     Print version info

Environment:
  PRAXIS_DB_TYPE       memory | sqlite | postgres (default: memory)
  PRAXIS_DB_CONN       backend connection string
  PRAXIS_HTTP_PORT     HTTP listen port (default: 8080)
  PRAXIS_API_TOKEN     bearer token required by /v1/* endpoints
  PRAXIS_MNEMOS_URL    Mnemos /v1/events URL
  PRAXIS_MNEMOS_TOKEN  Mnemos bearer token
`)
}

// --- shared bootstrap ---

type runtime struct {
	logger   *bolt.Logger
	cfg      config.Config
	repos    *ports.Repos
	exec     *executor.Executor
	reg      *capability.Registry
	auditSvc *audit.Service
	emitter  *outcome.Emitter
	metrics  *metrics
}

func bootstrap(ctx context.Context) (*runtime, func(), error) {
	logger := bolt.New(bolt.NewJSONHandler(os.Stdout))
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	repos, err := store.Open(ctx, logger, store.Config{Type: cfg.DBType, Conn: cfg.DBConn})
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if repos.Close != nil {
			_ = repos.Close()
		}
	}

	registry := capability.New()
	registerHandlers(registry)

	m := &metrics{}
	if err := loadPlugins(ctx, logger, cfg, registry, m); err != nil {
		return nil, cleanup, err
	}

	pol := policy.New(logger, repos.Policy)
	pol.SetMode(policy.Mode(cfg.PolicyMode))
	idem := idempotency.New(repos.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	})
	validator := schema.New()
	emitter := outcome.New(logger, repos.Outbox, outcome.Config{
		URL:   cfg.MnemosURL,
		Token: cfg.MnemosToken,
	})
	exec := executor.New(logger, registry, pol, idem, runner, validator,
		repos.Action, repos.Audit, emitter)
	auditSvc := audit.New(repos.Audit).WithRetention(cfg.AuditRetention)

	return &runtime{
		logger: logger, cfg: cfg, repos: repos, exec: exec, reg: registry,
		auditSvc: auditSvc, emitter: emitter, metrics: m,
	}, cleanup, nil
}

// runSighupReload listens for SIGHUP and re-runs the plugin pipeline
// when it arrives. Returns when ctx is cancelled. Phase 4: pairs with
// the fsnotify watcher for deployments where file events aren't
// available (containers, NFS, etc.).
func runSighupReload(ctx context.Context, rt *runtime) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			rt.logger.Info().Msg("SIGHUP received; rescanning plugins")
			if err := loadPlugins(ctx, rt.logger, rt.cfg, rt.reg, rt.metrics); err != nil {
				rt.logger.Error().Err(err).Msg("SIGHUP plugin rescan failed")
			}
		}
	}
}

// pluginRegistryLoader bridges the in-process *capability.Registry into
// plugin.Loader. Phase 4 M3.1.
type pluginRegistryLoader struct{ reg *capability.Registry }

func (l *pluginRegistryLoader) Register(r plugin.Registration) error {
	return l.reg.Register(r.Handler)
}

// loadPlugins runs the discover→verify→open→Load pipeline. Empty
// PRAXIS_PLUGIN_DIR is a no-op. Per-plugin failures log and continue
// in non-strict mode; PRAXIS_PLUGIN_STRICT=1 turns any per-plugin
// failure into a bootstrap error so production deployments fail fast
// rather than running with a partially populated registry.
func loadPlugins(ctx context.Context, logger *bolt.Logger, cfg config.Config, reg *capability.Registry, m *metrics) error {
	if cfg.PluginDir == "" {
		return nil
	}
	keys, err := plugin.LoadTrustedKeys(cfg.PluginTrustedKeys)
	if err != nil {
		return fmt.Errorf("load trusted plugin keys: %w", err)
	}
	res, err := plugin.RunPipeline(ctx, plugin.PipelineConfig{
		Dir:         cfg.PluginDir,
		TrustedKeys: keys,
		Loader:      &pluginRegistryLoader{reg: reg},
		Opener:      plugin.DefaultOpener{},
	})
	if err != nil {
		return fmt.Errorf("plugin pipeline: %w", err)
	}
	for _, e := range res.Errors {
		result := plugin.ClassifyError(e.Err)
		m.incPluginLoad(result)
		logger.Error().
			Str("plugin_dir", e.Dir).
			Str("result", result).
			Str("error", e.Err.Error()).
			Msg("plugin load failed")
	}
	for _, p := range res.Loaded {
		m.incPluginLoad(plugin.ResultSuccess)
		logger.Info().
			Str("plugin_dir", p.Dir).
			Str("name", p.Manifest.Name).
			Str("version", p.Manifest.Version).
			Str("abi", p.ABI).
			Msg("plugin loaded")
	}
	if cfg.PluginStrict && len(res.Errors) > 0 {
		return fmt.Errorf("plugin pipeline: %d plugin(s) failed to load and PRAXIS_PLUGIN_STRICT=1", len(res.Errors))
	}
	return nil
}

func registerHandlers(reg *capability.Registry) {
	_ = reg.Register(slackhandler.New(os.Getenv("SLACK_TOKEN")))
	_ = reg.Register(emailhandler.New(emailhandler.Config{
		Host:     os.Getenv("SMTP_HOST"),
		Port:     defaultEnv("SMTP_PORT", "587"),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     os.Getenv("SMTP_FROM"),
	}))
	_ = reg.Register(httphandler.New(httphandler.Config{}))
	ghCfg := githubhandler.Config{Token: os.Getenv("GITHUB_TOKEN")}
	_ = reg.Register(githubhandler.NewCreateIssue(ghCfg))
	_ = reg.Register(githubhandler.NewAddComment(ghCfg))
	linCfg := linearhandler.Config{Token: os.Getenv("LINEAR_TOKEN")}
	_ = reg.Register(linearhandler.NewCreateIssue(linCfg))
	_ = reg.Register(linearhandler.NewTransitionStatus(linCfg))
	_ = reg.Register(calendarhandler.New(defaultEnv("PRAXIS_PRODUCT_DOMAIN", "praxis.local")))
}

func defaultEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// --- subcommands ---

func runServe() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	go rt.emitter.Run(ctx)

	jobsRunner := jobs.New(rt.logger, rt.repos.Action, rt.exec, jobs.Config{})
	go jobsRunner.Run(ctx)

	if rt.cfg.PluginDir != "" && rt.cfg.PluginAutoreload {
		w, werr := plugin.NewWatcher(plugin.WatcherConfig{
			Root: rt.cfg.PluginDir,
			OnReload: func(pluginDir string) {
				rt.logger.Info().Str("plugin_dir", pluginDir).Msg("plugin change detected; reloading pipeline")
				if err := loadPlugins(ctx, rt.logger, rt.cfg, rt.reg, rt.metrics); err != nil {
					rt.logger.Error().Err(err).Msg("plugin reload failed")
				}
			},
		})
		if werr != nil {
			rt.logger.Error().Err(werr).Msg("plugin watcher disabled")
		} else {
			go w.Run(ctx)
		}
	}

	// SIGHUP forces a full plugin re-scan. Operators on file-watch-less
	// deployments (containers without inotify, NFS mounts, etc.) rely on
	// this to pick up rotated plugins. Idempotent: the pipeline runs
	// through verify+Load which is safe to repeat against unchanged
	// plugins (Go's plugin.Open caches the *Plugin handle so dlopen is
	// a no-op the second time).
	if rt.cfg.PluginDir != "" {
		go runSighupReload(ctx, rt)
	}

	if len(rt.cfg.AuditRetention) > 0 {
		sched := audit.NewScheduler(rt.auditSvc, rt.logger, audit.SchedulerConfig{
			InitialDelay: rt.cfg.AuditRetentionInitialDelay,
			Interval:     rt.cfg.AuditRetentionInterval,
		})
		sched.OnPurge = func(orgID string, deleted int64, err error) {
			result := "ok"
			if err != nil {
				result = "error"
			}
			rt.metrics.addAuditPurge(orgID, result, deleted)
		}
		go sched.Run(ctx)
	}

	mux := newMux(kernelDeps{
		logger: rt.logger, exec: rt.exec, registry: rt.reg, repos: rt.repos,
		auditSvc: rt.auditSvc,
		emitter:  rt.emitter, apiToken: rt.cfg.APIToken,
	}, rt.metrics)

	addr := fmt.Sprintf("%s:%d", rt.cfg.HTTPHost, rt.cfg.HTTPPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	rt.logger.Info().Str("addr", addr).Msg("praxis server listening")

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		rt.logger.Error().Err(err).Msg("server error")
		return 1
	case <-ctx.Done():
		rt.logger.Info().Msg("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return 0
}

func runMCP() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	go rt.emitter.Run(ctx)
	jobsRunner := jobs.New(rt.logger, rt.repos.Action, rt.exec, jobs.Config{})
	go jobsRunner.Run(ctx)

	srv := pmcp.Register(pmcp.Info{Name: "praxis", Version: Version}, rt.exec, generateID)
	if err := pmcp.ServeStdio(ctx, srv); err != nil {
		rt.logger.Error().Err(err).Msg("mcp serve")
		return 1
	}
	return 0
}

func runCaps(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: praxis caps [list|show <name>] [--org=<id>] [--team=<id>]")
		return 2
	}
	ctx := context.Background()
	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	positional, caller := parseCallerFlags(args)
	if len(positional) == 0 {
		fmt.Fprintln(os.Stderr, "usage: praxis caps [list|show <name>] [--org=<id>] [--team=<id>]")
		return 2
	}

	switch positional[0] {
	case "list":
		caps, err := rt.exec.ListCapabilitiesForCaller(ctx, caller)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, c := range caps {
			fmt.Println(c.Name)
		}
	case "show":
		if len(positional) < 2 {
			fmt.Fprintln(os.Stderr, "usage: praxis caps show <name> [--org=<id>] [--team=<id>]")
			return 2
		}
		c, err := rt.reg.GetCapabilityForCaller(positional[1], caller)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		printJSON(c)
	default:
		fmt.Fprintln(os.Stderr, "unknown caps subcommand:", positional[0])
		return 2
	}
	return 0
}

// parseCallerFlags extracts --org and --team flags from the argument
// stream and returns the positional remainder. Supports both
// `--org=<id>` and `--org <id>` forms; unknown flags pass through as
// positional so the caller can decide whether to reject them.
func parseCallerFlags(args []string) ([]string, domain.CallerRef) {
	var caller domain.CallerRef
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--org="):
			caller.OrgID = strings.TrimPrefix(a, "--org=")
		case a == "--org" && i+1 < len(args):
			caller.OrgID = args[i+1]
			i++
		case strings.HasPrefix(a, "--team="):
			caller.TeamID = strings.TrimPrefix(a, "--team=")
		case a == "--team" && i+1 < len(args):
			caller.TeamID = args[i+1]
			i++
		default:
			out = append(out, a)
		}
	}
	return out, caller
}

func runAction(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: praxis run <capability> <json> [--dry-run]")
		return 2
	}
	ctx := context.Background()
	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	capName := args[0]
	payloadJSON := "{}"
	dryRun := false
	for _, a := range args[1:] {
		if a == "--dry-run" {
			dryRun = true
			continue
		}
		if !strings.HasPrefix(a, "--") {
			payloadJSON = a
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		fmt.Fprintln(os.Stderr, "invalid payload JSON:", err)
		return 2
	}

	action := domain.Action{
		ID:         generateID(),
		Capability: capName,
		Payload:    payload,
		Caller:     domain.CallerRef{Type: "cli", ID: defaultEnv("USER", "user")},
	}
	if dryRun {
		sim, err := rt.exec.DryRun(ctx, action)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		printJSON(sim)
		return 0
	}
	res, err := rt.exec.Execute(ctx, action)
	printJSON(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runRevert(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: praxis revert <action-id>")
		return 2
	}
	ctx := context.Background()
	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	res, err := rt.exec.Revert(ctx, args[0])
	printJSON(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runLog(args []string) int {
	if len(args) < 2 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "usage: praxis log show <action-id>")
		return 2
	}
	ctx := context.Background()
	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	actionID := args[1]
	a, err := rt.repos.Action.Get(ctx, actionID)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	events, _ := rt.repos.Audit.ListForAction(ctx, actionID)

	out := map[string]any{
		"action": a,
		"audit":  events,
	}
	if lc, replayErr := audit.Replay(ctx, rt.repos.Audit, actionID); replayErr == nil {
		out["replayed_lifecycle"] = lc
	}
	printJSON(out)
	return 0
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println(string(b))
}
