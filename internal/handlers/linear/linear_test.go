package linear_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/handlers/linear"
)

func TestCreateIssue_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_xxx" {
			t.Errorf("auth=%s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		q, _ := body["query"].(string)
		if !strings.Contains(q, "issueCreate") {
			t.Errorf("query missing issueCreate")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"issueCreate":{"success":true,"issue":{"id":"abc","identifier":"PRX-1","title":"hi","url":"https://linear.app/issue/abc"}}}}`)
	}))
	defer srv.Close()

	h := linear.NewCreateIssue(linear.Config{Token: "lin_api_xxx", BaseURL: srv.URL})
	out, err := h.Execute(context.Background(), map[string]any{
		"team_id": "team-1", "title": "hi",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["identifier"] != "PRX-1" {
		t.Errorf("identifier=%v", out["identifier"])
	}
	if out["external_id"] != "abc" {
		t.Errorf("external_id=%v", out["external_id"])
	}
}

func TestCreateIssue_GraphQLErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errors":[{"message":"team not found"}]}`)
	}))
	defer srv.Close()

	h := linear.NewCreateIssue(linear.Config{Token: "tk", BaseURL: srv.URL})
	_, err := h.Execute(context.Background(), map[string]any{"team_id": "x", "title": "y"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "team not found") {
		t.Errorf("err=%v", err)
	}
}

func TestCreateIssue_RequiresFields(t *testing.T) {
	h := linear.NewCreateIssue(linear.Config{Token: "tk"})
	if _, err := h.Execute(context.Background(), map[string]any{"team_id": "x"}); err == nil {
		t.Error("expected error when title missing")
	}
}

func TestCreateIssue_DegradedNoToken(t *testing.T) {
	h := linear.NewCreateIssue(linear.Config{})
	out, err := h.Execute(context.Background(), map[string]any{"team_id": "x", "title": "y"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("expected simulated, got %v", out)
	}
}

func TestTransitionStatus_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"issueUpdate":{"success":true,"issue":{"id":"abc","identifier":"PRX-1","state":{"id":"s","name":"Done"}}}}}`)
	}))
	defer srv.Close()

	h := linear.NewTransitionStatus(linear.Config{Token: "tk", BaseURL: srv.URL})
	out, err := h.Execute(context.Background(), map[string]any{
		"issue_id": "abc", "state_id": "s",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["state"] != "Done" {
		t.Errorf("state=%v", out["state"])
	}
}

func TestSimulate_NoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("Simulate should not contact Linear")
	}))
	defer srv.Close()

	h := linear.NewCreateIssue(linear.Config{Token: "tk", BaseURL: srv.URL})
	out, err := h.Simulate(context.Background(), map[string]any{"team_id": "x", "title": "y"})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("simulated=%v", out["simulated"])
	}
}
