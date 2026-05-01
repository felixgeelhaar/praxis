package ipc_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/plugin/ipc"
)

func TestCodec_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	enc := ipc.NewCodec(&buf, &buf)

	params, err := ipc.EncodeParams(ipc.ExecuteParams{
		Capability: "echo",
		Payload:    map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	if err := enc.Send(ipc.Frame{ID: "1", Method: ipc.MethodExecute, Params: params}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got, err := enc.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got.ID != "1" || got.Method != ipc.MethodExecute {
		t.Errorf("got=%+v", got)
	}
	var ep ipc.ExecuteParams
	if err := ipc.DecodeParams(got.Params, &ep); err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if ep.Capability != "echo" || ep.Payload["text"] != "hello" {
		t.Errorf("ep=%+v", ep)
	}
}

func TestCodec_NewlineFramed(t *testing.T) {
	var buf bytes.Buffer
	enc := ipc.NewCodec(&buf, &buf)
	for i := 0; i < 3; i++ {
		_ = enc.Send(ipc.Frame{ID: "x", Result: []byte(`{}`)})
	}
	// Three frames should produce three newlines.
	if got := bytes.Count(buf.Bytes(), []byte{'\n'}); got != 3 {
		t.Errorf("newlines=%d want 3", got)
	}
}

func TestCodec_RecvEOFUnwrapped(t *testing.T) {
	c := ipc.NewCodec(bytes.NewReader(nil), io.Discard)
	_, err := c.Recv()
	if !errors.Is(err, io.EOF) {
		t.Errorf("err=%v want io.EOF", err)
	}
}

func TestCodec_ErrorFrameTransports(t *testing.T) {
	var buf bytes.Buffer
	c := ipc.NewCodec(&buf, &buf)
	_ = c.Send(ipc.Frame{ID: "2", Error: "bad capability"})
	got, err := c.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got.Error != "bad capability" {
		t.Errorf("Error=%q", got.Error)
	}
}
