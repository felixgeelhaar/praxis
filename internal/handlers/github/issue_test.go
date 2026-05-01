package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/handlers/github"
)

func TestCreateIssue_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/issues" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tk" {
			t.Errorf("missing auth")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "hello" {
			t.Errorf("title=%v", body["title"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"number":7,"html_url":"https://github.com/acme/widgets/issues/7"}`)
	}))
	defer srv.Close()

	h := github.NewCreateIssue(github.Config{Token: "tk", BaseURL: srv.URL})
	out, err := h.Execute(context.Background(), map[string]any{
		"owner": "acme", "repo": "widgets", "title": "hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["number"] != 7 {
		t.Errorf("number=%v", out["number"])
	}
	if out["external_id"] != "42" {
		t.Errorf("external_id=%v", out["external_id"])
	}
}

func TestCreateIssue_VendorErrorWith429RetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"abuse"}`)
	}))
	defer srv.Close()

	h := github.NewCreateIssue(github.Config{Token: "tk", BaseURL: srv.URL})
	_, err := h.Execute(context.Background(), map[string]any{
		"owner": "acme", "repo": "widgets", "title": "hi",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var ra *handlerrunner.RetryAfterError
	if !errors.As(err, &ra) {
		t.Fatalf("err not a RetryAfterError: %v", err)
	}
	if ra.Cooldown.Seconds() != 60 {
		t.Errorf("Cooldown=%s want 60s", ra.Cooldown)
	}
}

func TestCreateIssue_RequiresFields(t *testing.T) {
	h := github.NewCreateIssue(github.Config{Token: "tk", BaseURL: "https://example"})
	_, err := h.Execute(context.Background(), map[string]any{"owner": "acme"})
	if err == nil {
		t.Fatal("expected required-field error")
	}
}

func TestCreateIssue_DegradedNoToken(t *testing.T) {
	h := github.NewCreateIssue(github.Config{})
	out, err := h.Execute(context.Background(), map[string]any{
		"owner": "a", "repo": "b", "title": "t",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("expected simulated=true, got %v", out)
	}
}

func TestAddComment_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/issues/7/comments") {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":99,"html_url":"https://github.com/x/y/issues/7#issuecomment-99"}`)
	}))
	defer srv.Close()

	h := github.NewAddComment(github.Config{Token: "tk", BaseURL: srv.URL})
	out, err := h.Execute(context.Background(), map[string]any{
		"owner": "acme", "repo": "widgets", "issue_number": 7, "body": "thanks",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["id"] != int64(99) {
		t.Errorf("id=%v", out["id"])
	}
}

func TestAddComment_RequiresFields(t *testing.T) {
	h := github.NewAddComment(github.Config{Token: "tk", BaseURL: "https://x"})
	_, err := h.Execute(context.Background(), map[string]any{"owner": "a"})
	if err == nil {
		t.Fatal("expected required-field error")
	}
}

func TestSimulate_NoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("Simulate should not contact GitHub")
	}))
	defer srv.Close()

	h := github.NewCreateIssue(github.Config{Token: "tk", BaseURL: srv.URL})
	out, err := h.Simulate(context.Background(), map[string]any{
		"owner": "a", "repo": "b", "title": "t",
	})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("simulated=%v", out["simulated"])
	}
}
