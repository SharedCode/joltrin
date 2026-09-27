//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

func Test_StreamingData_Short(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, StoresFolders: storesFoldersDefault, CacheType: sop.Redis}
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStore_short", true, 100, "", sop.BigData, "")
	sds, _ := infs.NewStreamingDataStoreWithReplication[string](ctx, so, trans, nil)
	enc, _ := sds.Add(ctx, "v1")
	for i := 0; i < 3; i++ {
		enc.Encode(fmt.Sprintf("%d. chunk", i))
	}
	if err := trans.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Read back
	trans, _ = infs.NewTransactionWithReplication(ctx, to)
	trans.Begin(ctx)
	sds, _ = infs.OpenStreamingDataStoreWithReplication[string](ctx, "videoStore_short", trans, nil)
	if ok, _ := sds.FindOne(ctx, "v1"); !ok {
		t.Fatalf("not found")
	}
	dec, _ := sds.GetCurrentValue(ctx)
	var chunk string
	count := 0
	for {
		if err := dec.Decode(&chunk); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		count++
	}
	if count != 3 {
		t.Fatalf("got %d want 3", count)
	}
	if err := trans.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
