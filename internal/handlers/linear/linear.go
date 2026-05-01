// Package linear implements Linear capability handlers via the public
// GraphQL API at api.linear.app/graphql.
//
// Linear's GraphQL endpoint is single-URL; both create_issue and
// transition_status are POSTs with different `query` strings. We talk to
// it directly via net/http rather than pulling in a Linear SDK.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
)

const defaultEndpoint = "https://api.linear.app/graphql"

// Config carries Linear API credentials.
type Config struct {
	Token   string       // Linear personal API key
	BaseURL string       // default https://api.linear.app/graphql
	Client  *http.Client // injected for tests
}

func defaults(c Config) Config {
	if c.BaseURL == "" {
		c.BaseURL = defaultEndpoint
	}
	if c.Client == nil {
		c.Client = http.DefaultClient
	}
	return c
}

// CreateIssue handles the linear_create_issue capability.
type CreateIssue struct{ cfg Config }

// TransitionStatus handles the linear_transition_status capability.
type TransitionStatus struct{ cfg Config }

// NewCreateIssue constructs a CreateIssue handler.
func NewCreateIssue(cfg Config) *CreateIssue { return &CreateIssue{cfg: defaults(cfg)} }

// NewTransitionStatus constructs a TransitionStatus handler.
func NewTransitionStatus(cfg Config) *TransitionStatus { return &TransitionStatus{cfg: defaults(cfg)} }

// --- create_issue ---

func (h *CreateIssue) Name() string { return "linear_create_issue" }

func (h *CreateIssue) Capability() domain.Capability {
	return domain.Capability{
		Name:        "linear_create_issue",
		Description: "Create a Linear issue via the GraphQL API",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"team_id", "title"},
			"properties": map[string]any{
				"team_id":     map[string]any{"type": "string"},
				"title":       map[string]any{"type": "string", "minLength": 1},
				"description": map[string]any{"type": "string"},
				"priority":    map[string]any{"type": "integer", "minimum": 0, "maximum": 4},
				"assignee_id": map[string]any{"type": "string"},
				"label_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"id", "identifier"},
		},
		Permissions: []string{"linear:write"},
		Simulatable: true,
		Idempotent:  false,
	}
}

const createIssueMutation = `mutation IssueCreate($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue { id identifier title url }
  }
}`

func (h *CreateIssue) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	team, _ := payload["team_id"].(string)
	title, _ := payload["title"].(string)
	if team == "" || title == "" {
		return nil, errors.New("team_id and title are required")
	}
	if h.cfg.Token == "" {
		return map[string]any{
			"ok":         true,
			"simulated":  true,
			"id":         "sim-id",
			"identifier": "SIM-0",
			"url":        "https://linear.app/sim",
		}, nil
	}
	input := map[string]any{
		"teamId": team,
		"title":  title,
	}
	if v, ok := payload["description"].(string); ok && v != "" {
		input["description"] = v
	}
	if v, ok := payload["priority"]; ok {
		input["priority"] = v
	}
	if v, ok := payload["assignee_id"].(string); ok && v != "" {
		input["assigneeId"] = v
	}
	if v, ok := payload["label_ids"]; ok {
		input["labelIds"] = v
	}

	var out struct {
		Data struct {
			IssueCreate struct {
				Success bool `json:"success"`
				Issue   struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
					Title      string `json:"title"`
					URL        string `json:"url"`
				} `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
	}
	if err := graphQL(ctx, h.cfg, createIssueMutation, map[string]any{"input": input}, &out); err != nil {
		return nil, err
	}
	if !out.Data.IssueCreate.Success {
		return nil, errors.New("linear: issueCreate returned success=false")
	}
	issue := out.Data.IssueCreate.Issue
	return map[string]any{
		"ok":          true,
		"id":          issue.ID,
		"identifier":  issue.Identifier,
		"title":       issue.Title,
		"url":         issue.URL,
		"external_id": issue.ID,
	}, nil
}

func (h *CreateIssue) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	team, _ := payload["team_id"].(string)
	title, _ := payload["title"].(string)
	if team == "" || title == "" {
		return nil, errors.New("team_id and title are required")
	}
	return map[string]any{
		"ok":         true,
		"simulated":  true,
		"identifier": "SIM-0",
		"title":      title,
	}, nil
}

// --- transition_status ---

func (h *TransitionStatus) Name() string { return "linear_transition_status" }

func (h *TransitionStatus) Capability() domain.Capability {
	return domain.Capability{
		Name:        "linear_transition_status",
		Description: "Move a Linear issue to a new workflow state",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"issue_id", "state_id"},
			"properties": map[string]any{
				"issue_id": map[string]any{"type": "string"},
				"state_id": map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"id"},
		},
		Permissions: []string{"linear:write"},
		Simulatable: true,
		Idempotent:  true, // transitioning to a state already-applied is a no-op
	}
}

const transitionMutation = `mutation IssueUpdate($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) {
    success
    issue { id identifier state { id name } }
  }
}`

func (h *TransitionStatus) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	issueID, _ := payload["issue_id"].(string)
	stateID, _ := payload["state_id"].(string)
	if issueID == "" || stateID == "" {
		return nil, errors.New("issue_id and state_id are required")
	}
	if h.cfg.Token == "" {
		return map[string]any{"ok": true, "simulated": true, "id": issueID}, nil
	}
	var out struct {
		Data struct {
			IssueUpdate struct {
				Success bool `json:"success"`
				Issue   struct {
					ID    string `json:"id"`
					State struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"state"`
				} `json:"issue"`
			} `json:"issueUpdate"`
		} `json:"data"`
	}
	vars := map[string]any{"id": issueID, "input": map[string]any{"stateId": stateID}}
	if err := graphQL(ctx, h.cfg, transitionMutation, vars, &out); err != nil {
		return nil, err
	}
	if !out.Data.IssueUpdate.Success {
		return nil, errors.New("linear: issueUpdate returned success=false")
	}
	return map[string]any{
		"ok":          true,
		"id":          out.Data.IssueUpdate.Issue.ID,
		"state":       out.Data.IssueUpdate.Issue.State.Name,
		"external_id": out.Data.IssueUpdate.Issue.ID,
	}, nil
}

func (h *TransitionStatus) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	issueID, _ := payload["issue_id"].(string)
	stateID, _ := payload["state_id"].(string)
	if issueID == "" || stateID == "" {
		return nil, errors.New("issue_id and state_id are required")
	}
	return map[string]any{"ok": true, "simulated": true, "id": issueID, "state_id": stateID}, nil
}

// --- shared GraphQL plumbing ---

func graphQL(ctx context.Context, cfg Config, query string, variables map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("linear build: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", cfg.Token)
	}
	resp, err := cfg.Client.Do(req)
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		base := fmt.Errorf("linear HTTP %d: %s", resp.StatusCode, string(raw))
		return handlerrunner.WrapHTTPError(base, resp)
	}
	// GraphQL surfaces user errors in `errors` even on 200.
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear: %s", envelope.Errors[0].Message)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("linear decode: %w", err)
	}
	return nil
}

var (
	_ capability.Handler = (*CreateIssue)(nil)
	_ capability.Handler = (*TransitionStatus)(nil)
)
