package idempotency

import (
	"context"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

type Keeper struct {
	repo ports.IdempotencyRepo
}

func New(repo ports.IdempotencyRepo) *Keeper {
	return &Keeper{repo: repo}
}

func (k *Keeper) Check(ctx context.Context, key string) (*domain.Result, error) {
	return k.repo.Lookup(ctx, key)
}

func (k *Keeper) Remember(ctx context.Context, key string, res domain.Result) error {
	// Default TTL: 24 hours
	return k.repo.Remember(ctx, key, res, 86400)
}
