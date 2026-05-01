// Command praxis-pluginhost loads exactly one Praxis plugin .so file
// and serves Praxis's plugin IPC protocol over stdin/stdout. It is
// the worker process spawned by Praxis's out-of-process loader.
//
// Usage:
//
//	praxis-pluginhost <path-to-plugin.so>
//
// The parent (Praxis) writes one JSON frame per line to stdin and
// reads one frame per line off stdout. Frame correlation is by ID;
// the host serves frames concurrently and never reorders responses
// across the pipe relative to its read order.
//
// Phase 4 out-of-process loader (M3.1).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	praxisplugin "github.com/felixgeelhaar/praxis/internal/plugin"
	"github.com/felixgeelhaar/praxis/internal/plugin/ipc"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: praxis-pluginhost <plugin.so>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pluginhost:", err)
		os.Exit(1)
	}
}

// run is the testable entry point. It dlopens the .so, looks up the
// Plugin symbol, and serves IPC over the supplied reader/writer until
// the parent closes stdin.
func run(artefact string, in io.Reader, out io.Writer) error {
	p, err := praxisplugin.DefaultOpener{}.Open(artefact)
	if err != nil {
		return fmt.Errorf("load plugin: %w", err)
	}
	if got := p.ABI(); got != praxisplugin.ABIVersion {
		return fmt.Errorf("plugin ABI mismatch: runtime=%s plugin=%s", praxisplugin.ABIVersion, got)
	}

	regs, err := p.Capabilities(context.Background())
	if err != nil {
		return fmt.Errorf("capabilities: %w", err)
	}
	srv := &server{
		plugin: p,
		caps:   indexRegistrations(regs),
	}
	codec := ipc.NewCodec(in, out)
	return srv.serve(context.Background(), codec)
}

func indexRegistrations(regs []praxisplugin.Registration) map[string]praxisplugin.Registration {
	out := map[string]praxisplugin.Registration{}
	for _, r := range regs {
		out[r.Capability.Name] = r
	}
	return out
}

// server holds the loaded plugin and routes incoming frames. Frames
// are dispatched on per-request goroutines so a slow Execute does not
// block subsequent calls; the IPC codec serialises writes internally.
type server struct {
	plugin praxisplugin.Plugin
	caps   map[string]praxisplugin.Registration

	wg sync.WaitGroup
}

func (s *server) serve(ctx context.Context, codec *ipc.Codec) error {
	for {
		f, err := codec.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.wg.Wait()
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func(f ipc.Frame) {
			defer s.wg.Done()
			s.dispatch(ctx, codec, f)
		}(f)
	}
}

func (s *server) dispatch(ctx context.Context, codec *ipc.Codec, f ipc.Frame) {
	resp := ipc.Frame{ID: f.ID}
	switch f.Method {
	case ipc.MethodManifest:
		resp.Result = s.manifestResponse()
	case ipc.MethodCapabilities:
		resp.Result = s.capabilitiesResponse()
	case ipc.MethodExecute:
		resp.Result, resp.Error = s.executeResponse(ctx, f.Params, false)
	case ipc.MethodSimulate:
		resp.Result, resp.Error = s.executeResponse(ctx, f.Params, true)
	default:
		resp.Error = "unknown method: " + f.Method
	}
	if err := codec.Send(resp); err != nil {
		// stdout is broken: print to stderr and let the parent decide
		// what to do (typically: kill the process).
		fmt.Fprintln(os.Stderr, "pluginhost: send response:", err)
	}
}

func (s *server) manifestResponse() []byte {
	m := s.plugin.Manifest()
	raw, _ := ipc.EncodeResult(ipc.ManifestResult{
		Name:        m.Name,
		Version:     m.Version,
		Author:      m.Author,
		Description: m.Description,
		Homepage:    m.Homepage,
		License:     m.License,
	})
	return raw
}

func (s *server) capabilitiesResponse() []byte {
	out := make([]ipc.CapabilityDescriptor, 0, len(s.caps))
	for _, r := range s.caps {
		out = append(out, ipc.CapabilityDescriptor{
			Name:         r.Capability.Name,
			Description:  r.Capability.Description,
			InputSchema:  r.Capability.InputSchema,
			OutputSchema: r.Capability.OutputSchema,
			Permissions:  r.Capability.Permissions,
			Simulatable:  r.Capability.Simulatable,
			Idempotent:   r.Capability.Idempotent,
		})
	}
	raw, _ := ipc.EncodeResult(ipc.CapabilitiesResult{Capabilities: out})
	return raw
}

func (s *server) executeResponse(ctx context.Context, params []byte, simulate bool) ([]byte, string) {
	var ep ipc.ExecuteParams
	if err := ipc.DecodeParams(params, &ep); err != nil {
		return nil, "decode params: " + err.Error()
	}
	r, ok := s.caps[ep.Capability]
	if !ok {
		return nil, "unknown capability: " + ep.Capability
	}
	var (
		out map[string]any
		err error
	)
	if simulate {
		out, err = r.Handler.Simulate(ctx, ep.Payload)
	} else {
		out, err = r.Handler.Execute(ctx, ep.Payload)
	}
	if err != nil {
		return nil, err.Error()
	}
	raw, _ := ipc.EncodeResult(ipc.ExecuteResult{Output: out})
	return raw, ""
}
