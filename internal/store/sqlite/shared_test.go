package sqlite_test

import (
	"testing"

	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/storetest"
)

func runShared(t *testing.T, factory func(t *testing.T) *ports.Repos) {
	t.Helper()
	storetest.RunSuite(t, factory)
}
