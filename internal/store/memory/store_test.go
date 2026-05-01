package memory_test

import (
	"testing"

	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
	"github.com/felixgeelhaar/praxis/internal/store/storetest"
)

func TestMemoryBackend(t *testing.T) {
	storetest.RunSuite(t, func(t *testing.T) *ports.Repos {
		t.Helper()
		return memory.New()
	})
}
