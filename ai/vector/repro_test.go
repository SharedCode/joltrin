package vector

import (
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func TestRepro(t *testing.T) {
	fmt.Printf("NoCache: %d\n", sop.NoCache)
	fmt.Printf("InMemory: %d\n", sop.InMemory)
	fmt.Printf("Redis: %d\n", sop.Redis)

	db, _ := database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{t.TempDir()},
	})

	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType})
	fmt.Printf("Cache type: %T\n", cache)

	if cache == nil {
		t.Fatal("Cache is nil")
	}
}
