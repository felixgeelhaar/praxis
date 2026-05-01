package audit

import (
	"context"
	"encoding/json"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

type Service struct {
	repo ports.AuditRepo
}

func New(repo ports.AuditRepo) *Service {
	return &Service{repo: repo}
}

func (s *Service) Append(ctx context.Context, event domain.AuditEvent) error {
	return s.repo.Append(ctx, event)
}

func (s *Service) ListForAction(ctx context.Context, actionID string) ([]domain.AuditEvent, error) {
	return s.repo.ListForAction(ctx, actionID)
}

func (s *Service) Search(ctx context.Context, q ports.AuditQuery) ([]domain.AuditEvent, error) {
	return s.repo.Search(ctx, q)
}

func (s *Service) Format(event domain.AuditEvent) string {
	data, _ := json.MarshalIndent(event.Detail, "", "  ")
	return string(data)
}
