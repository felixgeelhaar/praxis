// Package github implements GitHub capability handlers via the REST v3
// API. Handlers talk to api.github.com directly so the dependency surface
// stays at net/http; vendor SDKs would couple every Praxis deployment to
// the SDK's release cadence.
//
// Idempotency:
//
//   - create_issue: GitHub does not surface a server-side idempotency
//     mechanism for issue creation. The Praxis-layer IdempotencyKeeper
//     short-circuits same-Action.ID re-execution; callers should pass a
//     stable IdempotencyKey on Action so retries don't double-create.
//   - add_comment: same Praxis-layer guarantee; comment bodies are not
//     deduped by GitHub. Re-execution with the same Action.ID returns
//     the cached Result.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
)

// Config carries the GitHub credentials and base URL.
type Config struct {
	Token   string       // PAT or installation token
	BaseURL string       // default https://api.github.com
	Client  *http.Client // injected for tests
}

// CreateIssue handles the github_create_issue capability.
type CreateIssue struct{ cfg Config }

// AddComment handles the github_add_comment capability.
type AddComment struct{ cfg Config }

// NewCreateIssue constructs a CreateIssue handler. With Token empty the
// handler runs in degraded mode (Execute returns simulated success).
func NewCreateIssue(cfg Config) *CreateIssue { return &CreateIssue{cfg: defaults(cfg)} }

// NewAddComment constructs an AddComment handler.
func NewAddComment(cfg Config) *AddComment { return &AddComment{cfg: defaults(cfg)} }

func defaults(c Config) Config {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	if c.Client == nil {
		c.Client = http.DefaultClient
	}
	return c
}

// --- create_issue ---

func (h *CreateIssue) Name() string { return "github_create_issue" }

func (h *CreateIssue) Capability() domain.Capability {
	return domain.Capability{
		Name:        "github_create_issue",
		Description: "Open an issue against a GitHub repository",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"owner", "repo", "title"},
			"properties": map[string]any{
				"owner":     map[string]any{"type": "string"},
				"repo":      map[string]any{"type": "string"},
				"title":     map[string]any{"type": "string", "minLength": 1},
				"body":      map[string]any{"type": "string"},
				"labels":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"assignees": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"number", "url"},
		},
		Permissions: []string{"github:write"},
		Simulatable: true,
		Idempotent:  false,
	}
}

func (h *CreateIssue) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	owner, repo, body, err := requireIssueFields(payload)
	if err != nil {
		return nil, err
	}
	if h.cfg.Token == "" {
		return simulatedIssue(payload), nil
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues", h.cfg.BaseURL, owner, repo)
	resp, raw, err := postJSON(ctx, h.cfg.Client, url, h.cfg.Token, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ghError(resp, raw)
	}
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		ID      int64  `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("github decode: %w", err)
	}
	return map[string]any{
		"ok":          true,
		"number":      out.Number,
		"url":         out.HTMLURL,
		"external_id": fmt.Sprintf("%d", out.ID),
	}, nil
}

func (h *CreateIssue) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	if _, _, _, err := requireIssueFields(payload); err != nil {
		return nil, err
	}
	return simulatedIssue(payload), nil
}

func simulatedIssue(payload map[string]any) map[string]any {
	owner, _ := payload["owner"].(string)
	repo, _ := payload["repo"].(string)
	return map[string]any{
		"ok":        true,
		"simulated": true,
		"number":    0,
		"url":       fmt.Sprintf("https://github.com/%s/%s/issues/0", owner, repo),
	}
}

func requireIssueFields(payload map[string]any) (owner, repo string, body map[string]any, err error) {
	owner, _ = payload["owner"].(string)
	repo, _ = payload["repo"].(string)
	title, _ := payload["title"].(string)
	if owner == "" || repo == "" {
		return "", "", nil, errors.New("owner and repo are required")
	}
	if title == "" {
		return "", "", nil, errors.New("title is required")
	}
	body = map[string]any{"title": title}
	if b, ok := payload["body"].(string); ok && b != "" {
		body["body"] = b
	}
	if v, ok := payload["labels"]; ok {
		body["labels"] = v
	}
	if v, ok := payload["assignees"]; ok {
		body["assignees"] = v
	}
	return owner, repo, body, nil
}

// --- add_comment ---

func (h *AddComment) Name() string { return "github_add_comment" }

func (h *AddComment) Capability() domain.Capability {
	return domain.Capability{
		Name:        "github_add_comment",
		Description: "Comment on a GitHub issue or pull request",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"owner", "repo", "issue_number", "body"},
			"properties": map[string]any{
				"owner":        map[string]any{"type": "string"},
				"repo":         map[string]any{"type": "string"},
				"issue_number": map[string]any{"type": "integer", "minimum": 1},
				"body":         map[string]any{"type": "string", "minLength": 1},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"id", "url"},
		},
		Permissions: []string{"github:write"},
		Simulatable: true,
		Idempotent:  false,
	}
}

func (h *AddComment) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	owner, repo, issueNum, body, err := requireCommentFields(payload)
	if err != nil {
		return nil, err
	}
	if h.cfg.Token == "" {
		return map[string]any{
			"ok":        true,
			"simulated": true,
			"id":        0,
			"url":       fmt.Sprintf("https://github.com/%s/%s/issues/%d#simulated", owner, repo, issueNum),
		}, nil
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", h.cfg.BaseURL, owner, repo, issueNum)
	resp, raw, err := postJSON(ctx, h.cfg.Client, url, h.cfg.Token, map[string]any{"body": body})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ghError(resp, raw)
	}
	var out struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("github decode: %w", err)
	}
	return map[string]any{
		"ok":          true,
		"id":          out.ID,
		"url":         out.HTMLURL,
		"external_id": fmt.Sprintf("%d", out.ID),
	}, nil
}

func (h *AddComment) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	owner, repo, issueNum, _, err := requireCommentFields(payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":        true,
		"simulated": true,
		"url":       fmt.Sprintf("https://github.com/%s/%s/issues/%d#simulated", owner, repo, issueNum),
	}, nil
}

func requireCommentFields(payload map[string]any) (owner, repo string, issueNum int, body string, err error) {
	owner, _ = payload["owner"].(string)
	repo, _ = payload["repo"].(string)
	body, _ = payload["body"].(string)
	switch v := payload["issue_number"].(type) {
	case float64:
		issueNum = int(v)
	case int:
		issueNum = v
	}
	if owner == "" || repo == "" || issueNum <= 0 || body == "" {
		return "", "", 0, "", errors.New("owner, repo, issue_number, body are required")
	}
	return owner, repo, issueNum, body, nil
}

// --- shared HTTP plumbing ---

func postJSON(ctx context.Context, client *http.Client, url, token string, body any) (*http.Response, []byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("github encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw, nil
}

// ghError builds a typed error from a non-2xx GitHub response. When
// Retry-After is present (rate-limited / abuse detection), the cooldown
// is propagated via handlerrunner.WrapHTTPError so the runner's retry
// strategy honours it.
func ghError(resp *http.Response, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	base := fmt.Errorf("github HTTP %d: %s", resp.StatusCode, msg)
	return handlerrunner.WrapHTTPError(base, resp)
}

var (
	_ capability.Handler = (*CreateIssue)(nil)
	_ capability.Handler = (*AddComment)(nil)
)
