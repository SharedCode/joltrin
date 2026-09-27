package main

import (
	"context"
	"fmt"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func main() {
	opts := sop.DatabaseOptions{StoresFolders: []string{"/tmp/sop_data/db", "/tmp/sop_data/db1", "/tmp/sop_data/db2"}}
	db := database.NewDatabase(opts)
	ctx := context.Background()
	trans, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		panic(err)
	}
	defer trans.Rollback(ctx)
	allStores, _ := trans.GetStores(ctx)
	for _, s := range allStores {
		fmt.Println(s)
	}
}
