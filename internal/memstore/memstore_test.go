package memstore_test

import (
	"testing"

	"github.com/drellem2/macguffin/internal/memstore"
	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/storetest"
)

// The fake is held to the same contract as the filesystem backend.
func TestContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Backend { return memstore.New() })
}
