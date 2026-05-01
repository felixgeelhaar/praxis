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
	emailhandler "github.com/felixgeelhaar/praxis/internal/handlers/email"
	httphandler "github.com/felixgeelhaar/praxis/internal/handlers/http"
	slackhandler "github.com/felixgeelhaar/praxis/internal/handlers/slack"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/jobs"
	pmcp "github.com/felixgeelhaar/praxis/internal/mcp"
	"github.com/felixgeelhaar/praxis/internal/outcome"
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
  praxis caps list                   List registered capabilities
  praxis caps show <name>            Show one capability
  praxis run <cap> <json> [--dry-run] Execute or simulate a capability
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
	logger  *bolt.Logger
	cfg     config.Config
	repos   *ports.Repos
	exec    *executor.Executor
	reg     *capability.Registry
	emitter *outcome.Emitter
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

	return &runtime{
		logger: logger, cfg: cfg, repos: repos, exec: exec, reg: registry, emitter: emitter,
	}, cleanup, nil
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

	m := &metrics{}
	mux := newMux(kernelDeps{
		logger: rt.logger, exec: rt.exec, registry: rt.reg, repos: rt.repos,
		emitter: rt.emitter, apiToken: rt.cfg.APIToken,
	}, m)

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
		fmt.Fprintln(os.Stderr, "usage: praxis caps [list|show <name>]")
		return 2
	}
	ctx := context.Background()
	rt, cleanup, err := bootstrap(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap:", err)
		return 1
	}
	defer cleanup()

	switch args[0] {
	case "list":
		caps, err := rt.exec.ListCapabilities(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, c := range caps {
			fmt.Println(c.Name)
		}
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: praxis caps show <name>")
			return 2
		}
		c, err := rt.reg.GetCapability(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		printJSON(c)
	default:
		fmt.Fprintln(os.Stderr, "unknown caps subcommand:", args[0])
		return 2
	}
	return 0
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
