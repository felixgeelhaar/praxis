// Package ipc carries the wire protocol between Praxis and a
// praxis-pluginhost child process. The transport is line-delimited
// JSON over stdin/stdout — no protobuf, no gRPC, no third-party
// runtime dependency. Each line is one Frame; request and response
// share the same shape and are correlated by ID.
//
// Phase 4 out-of-process loader.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Method enumerates the four RPCs the host binary serves. Strings are
// part of the wire contract; never rename without bumping a protocol
// version (this protocol has none yet — additive RPCs are safe).
const (
	MethodManifest     = "manifest"
	MethodCapabilities = "capabilities"
	MethodExecute      = "execute"
	MethodSimulate     = "simulate"
)

// Frame is the one-line envelope. Either Method+Params is set
// (request) or Result/Error is set (response). ID correlates the pair;
// the parent owns ID generation.
type Frame struct {
	ID     string          `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// CapabilityDescriptor is the minimal capability shape transferred
// over IPC. Mirrors the fields the parent needs to reconstruct a
// domain.Capability without pulling the full struct (and its non-JSON
// fields) across the boundary.
type CapabilityDescriptor struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	InputSchema  any      `json:"input_schema,omitempty"`
	OutputSchema any      `json:"output_schema,omitempty"`
	Permissions  []string `json:"permissions,omitempty"`
	Simulatable  bool     `json:"simulatable,omitempty"`
	Idempotent   bool     `json:"idempotent,omitempty"`
}

// ManifestParams is the request body for MethodManifest. No fields
// today; reserved for future extension.
type ManifestParams struct{}

// ManifestResult is the response body for MethodManifest. Mirrors
// plugin.Manifest.
type ManifestResult struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author,omitempty"`
	Description string `json:"description,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`
}

// CapabilitiesResult lists the capabilities the loaded plugin
// declares.
type CapabilitiesResult struct {
	Capabilities []CapabilityDescriptor `json:"capabilities"`
}

// ExecuteParams identifies which capability to call and carries its
// JSON-encoded payload.
type ExecuteParams struct {
	Capability string         `json:"capability"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// ExecuteResult is the handler's output map.
type ExecuteResult struct {
	Output map[string]any `json:"output,omitempty"`
}

// Codec wraps a paired Reader/Writer with newline-framing and a write
// mutex so concurrent goroutines can call Send safely. Decode is
// expected to run in a single dispatcher goroutine on each side.
type Codec struct {
	dec *json.Decoder
	enc *bufio.Writer
	wmu sync.Mutex
}

// NewCodec wraps r and w as a JSON line-delimited codec. The decoder
// uses json.Decoder's streaming behaviour so it advances one frame at
// a time without buffering the rest of the stream.
func NewCodec(r io.Reader, w io.Writer) *Codec {
	return &Codec{
		dec: json.NewDecoder(r),
		enc: bufio.NewWriter(w),
	}
}

// Send marshals a Frame and writes it as one line. The trailing
// newline keeps line-buffered child processes happy and lets a human
// `tail` the IPC stream during debugging.
func (c *Codec) Send(f Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if _, err := c.enc.Write(b); err != nil {
		return err
	}
	if err := c.enc.WriteByte('\n'); err != nil {
		return err
	}
	return c.enc.Flush()
}

// Recv reads one Frame from the stream. EOF surfaces as
// io.EOF unwrapped so the caller can branch on errors.Is.
func (c *Codec) Recv() (Frame, error) {
	var f Frame
	if err := c.dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return Frame{}, io.EOF
		}
		return Frame{}, fmt.Errorf("decode frame: %w", err)
	}
	return f, nil
}

// Encode/Decode helpers keep call sites free of json.Marshal churn.

// EncodeParams serialises a typed params struct into RawMessage for
// embedding in a Frame.
func EncodeParams(v any) (json.RawMessage, error) { return json.Marshal(v) }

// DecodeParams unmarshals a Frame's Params into a typed struct.
func DecodeParams(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// EncodeResult is EncodeParams for response payloads.
func EncodeResult(v any) (json.RawMessage, error) { return json.Marshal(v) }

// DecodeResult is DecodeParams for response payloads.
func DecodeResult(raw json.RawMessage, v any) error { return DecodeParams(raw, v) }
