package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/felixgeelhaar/praxis/client"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

func newServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func TestListCapabilities(t *testing.T) {
	srv := newServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/capabilities" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []domain.Capability{{Name: "echo"}},
		})
	})
	defer srv.Close()

	c := client.New(srv.URL)
	caps, err := c.ListCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(caps) != 1 || caps[0].Name != "echo" {
		t.Errorf("unexpected caps: %v", caps)
	}
}

func TestExecute_AuthHeader(t *testing.T) {
	srv := newServer(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tk" {
			t.Errorf("Authorization=%q want Bearer tk", got)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(domain.Result{Status: domain.StatusSucceeded})
	})
	defer srv.Close()

	c := client.New(srv.URL, client.WithToken("tk"))
	res, err := c.Execute(context.Background(), domain.Action{ID: "a", Capability: "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != domain.StatusSucceeded {
		t.Errorf("Status=%s want succeeded", res.Status)
	}
}

func TestExecute_RetryOn5xxThenSuccess(t *testing.T) {
	var hits int32
	srv := newServer(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(domain.Result{Status: domain.StatusSucceeded})
	})
	defer srv.Close()

	c := client.New(srv.URL)
	res, err := c.Execute(context.Background(), domain.Action{Capability: "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != domain.StatusSucceeded {
		t.Errorf("Status=%s want succeeded", res.Status)
	}
	if atomic.LoadInt32(&hits) < 3 {
		t.Errorf("expected ≥3 hits (retries), got %d", hits)
	}
}

func TestExecute_FailFastOn4xx(t *testing.T) {
	var hits int32
	srv := newServer(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "bad", http.StatusBadRequest)
	})
	defer srv.Close()

	c := client.New(srv.URL)
	_, err := c.Execute(context.Background(), domain.Action{Capability: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *client.APIError, got %T", err)
	}
	if apiErr.Status != 400 {
		t.Errorf("Status=%d want 400", apiErr.Status)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("4xx should fail fast: hits=%d want 1", hits)
	}
}

func TestDryRun(t *testing.T) {
	srv := newServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/actions/a-1/dry-run" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(domain.Simulation{ActionID: "a-1", Reversible: true})
	})
	defer srv.Close()

	c := client.New(srv.URL)
	sim, err := c.DryRun(context.Background(), domain.Action{ID: "a-1", Capability: "x"})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !sim.Reversible {
		t.Errorf("Reversible=false")
	}
}

func TestDryRun_RequiresID(t *testing.T) {
	c := client.New("http://localhost:0")
	_, err := c.DryRun(context.Background(), domain.Action{Capability: "x"})
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestGetAction_NotFound(t *testing.T) {
	srv := newServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	})
	defer srv.Close()

	c := client.New(srv.URL)
	_, err := c.GetAction(context.Background(), "abc")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Errorf("expected 404 APIError, got %v", err)
	}
}
