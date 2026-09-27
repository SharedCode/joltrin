//go:build integration
// +build integration

package integrationtests

import (
	"fmt"
	"io"
	"testing"

	"github.com/sharedcode/joltrin/incfs"
	"github.com/sharedcode/joltrin/v5"
	sd "github.com/sharedcode/joltrin/v5/streamingdata"
)

func Test_StreamingDataStoreInvalidCases(t *testing.T) {
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)

	// Empty Store get/update methods test cases.
	so := sop.ConfigureStore("xyz", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	if _, err := sds.GetCurrentValue(ctx); err == nil {
		t.Errorf("GetCurrentValue on empty btree failed, got nil want err")
	}
	if _, err := sds.UpdateCurrentValue(ctx); err == nil {
		t.Errorf("UpdateCurrentValue on empty btree failed, got nil want err")
	}
	if _, err := sds.RemoveCurrentItem(ctx); err == nil {
		t.Errorf("RemoveCurrentItem on empty btree failed, got nil want err")
	}
}

func Test_StreamingDataStoreBasicUse(t *testing.T) {
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStore", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo")
	for i := 0; i < 10; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 10MB.", i))
	}
	trans.Commit(ctx)

	// Read back the data. Pass false on 2nd argument will toggle to a "reader" transaction.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)

	ok, _ := sds.FindOne(ctx, "fooVideo")
	if !ok {
		t.Errorf("FindOne('fooVideo') failed, got not found, want found")
	}
	decoder, _ := sds.GetCurrentValue(ctx)
	var target string
	i := 0
	for {
		i++
		if err := decoder.Decode(&target); err != nil {
			if err != io.EOF {
				t.Error(err)
			}
			break
		}
		fmt.Println(target)
	}
	if i != 11 {
		t.Errorf("Decoder failed, got %d, want 10.", i)
	}
	// Commit on "reader" transaction will ensure that data you read did not change on entire
	// transaction session until commit time. If other transaction did change the data read,
	// Commit on the reader will return an error to reflect that data consistency conflict.
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Reader transaction commit failed, details: %v", err)
	}
}

func Test_StreamingDataStoreMultipleItems(t *testing.T) {
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStoreM", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo")
	for i := 0; i < 10; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 12MB.", i))
	}
	encoder, _ = sds.Add(ctx, "fooVideo2")
	for i := 0; i < 20; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 10MB.", i))
	}
	trans.Commit(ctx)

	// Read back the data. Pass false on 2nd argument will toggle to a "reader" transaction.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForReading, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)

	ok, _ := sds.FindOne(ctx, "fooVideo")
	if !ok {
		t.Errorf("FindOne('fooVideo') failed, got not found, want found")
	}
	decoder, _ := sds.GetCurrentValue(ctx)
	var target string
	i := 0
	for {
		i++
		if err := decoder.Decode(&target); err != nil {
			if err != io.EOF {
				t.Error(err)
			}
			break
		}
		fmt.Println(target)
	}
	if i != 11 {
		t.Errorf("Decoder failed, got %d, want 10.", i)
	}
	// Commit on "reader" transaction will ensure that data you read did not change on entire
	// transaction session until commit time. If other transaction did change the data read,
	// Commit on the reader will return an error to reflect that data consistency conflict.
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Reader transaction commit failed, details: %v", err)
	}
}

func Test_StreamingDataStoreDeleteAnItem(t *testing.T) {
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStoreD", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo")
	for i := 0; i < 10; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 12MB.", i))
	}
	encoder, _ = sds.Add(ctx, "fooVideo2")
	for i := 0; i < 20; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 10MB.", i))
	}
	encoder, _ = sds.Add(ctx, "fooVideo3")
	for i := 0; i < 5; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 10MB.", i))
	}
	trans.Commit(ctx)

	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	sds, _ = incfs.OpenStreamingDataStore[string](ctx, "videoStoreD", trans, nil)

	ok, _ := sds.Remove(ctx, "fooVideo2")
	if !ok {
		t.Errorf("Remove('fooVideo2') failed, got false, want true")
	}
	sds.FindOne(ctx, "fooVideo3")
	decoder, _ := sds.GetCurrentValue(ctx)
	var target string
	i := 0
	for {
		i++
		if err := decoder.Decode(&target); err != nil {
			if err != io.EOF {
				t.Error(err)
			}
			break
		}
		fmt.Println(target)
	}
	if i != 6 {
		t.Errorf("Decoder failed, got %d, want 6.", i)
	}
	// Commit on "reader" transaction will ensure that data you read did not change on entire
	// transaction session until commit time. If other transaction did change the data read,
	// Commit on the reader will return an error to reflect that data consistency conflict.
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Reader transaction commit failed, details: %v", err)
	}
}

func Test_StreamingDataStoreBigDataUpdate(t *testing.T) {
	// Upload the video.
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStoreU", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo2")
	for i := 0; i < 10; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 10MB.", i))
	}
	trans.Commit(ctx)

	// Update the video.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ = sds.Update(ctx, "fooVideo2")
	chunkCount := 9
	for i := 0; i < chunkCount; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 15MB.", i))
	}
	// Close the "update" encoder to cleanup mis-aligned chunks.
	encoder.Close()
	trans.Commit(ctx)

	// Read back the video.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForReading, MaxTime: -1})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)

	ok, _ := sds.FindOne(ctx, "fooVideo2")
	if !ok {
		t.Errorf("FindOne('fooVideo') failed, got not found, want found")
	}
	decoder, _ := sds.GetCurrentValue(ctx)
	var target string

	for i := 0; i < 15; i++ {
		if i > chunkCount {
			t.Errorf("Failed decoding video, got %d want %d", i, chunkCount)
		}
		if err := decoder.Decode(&target); err != nil {
			if err == io.EOF {
				break
			}
			t.Error(err)
			break
		}
		fmt.Println(target)
	}
	// Commit on "reader" transaction will ensure that data you read did not change on entire
	// transaction session until commit time. If other transaction did change the data read,
	// Commit on the reader will return an error to reflect that data consistency conflict.
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Reader transaction commit failed, details: %v", err)
	}
}

func Test_StreamingDataStoreUpdateWithCountCheck(t *testing.T) {
	// Upload the video.
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStore2", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo1")
	encodeVideo(encoder, 50)
	trans.Commit(ctx)

	// Update the video.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ = sds.Update(ctx, "fooVideo1")
	encodeVideo(encoder, 5)
	// Important to close the encoder, otherwise, cleanup will not happen.
	encoder.Close()

	if sds.Count() != 5 {
		t.Errorf("Failed Update, got %d, want %d", sds.Count(), 5)
	}
	trans.Commit(ctx)
}

func Test_StreamingDataStoreUpdateExtend(t *testing.T) {
	// Upload the video.
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStore4", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo3")
	encodeVideo(encoder, 5)
	trans.Commit(ctx)

	// Update the video.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ = sds.Update(ctx, "fooVideo3")
	encodeVideo(encoder, 7)
	// Since we updated with 7 chunks, 2 longer than existing, Close will not do anything.
	// But call it anyway as part of "standard" for update encoder.
	encoder.Close()

	if sds.Count() != 7 {
		t.Errorf("Failed Update, got %d, want %d", sds.Count(), 7)
	}
	trans.Commit(ctx)
}

func Test_StreamingDataStoreUpdate(t *testing.T) {
	// Upload the video.
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStore5", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ := sds.Add(ctx, "fooVideo")
	encodeVideo(encoder, 5)
	trans.Commit(ctx)

	// Update the video.
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	sds, _ = incfs.NewStreamingDataStore[string](ctx, so, trans, nil)
	encoder, _ = sds.Update(ctx, "fooVideo")
	encodeVideo(encoder, 5)
	encoder.Close()

	if sds.Count() != 5 {
		t.Errorf("Failed Update, got %d, want %d", sds.Count(), 5)
	}
	trans.Commit(ctx)
}

func Test_StreamingDataStoreDelete(t *testing.T) {
	// Upload the video.
	trans, _ := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)
	so := sop.ConfigureStore("videoStore3", true, 100, "", sop.BigData, dataPath)
	sds, _ := incfs.NewStreamingDataStore[string](ctx, so, trans, nil)

	encoder, _ := sds.Add(ctx, "fooVideo1")
	encodeVideo(encoder, 50)

	encoder, _ = sds.Add(ctx, "fooVideo2")
	encodeVideo(encoder, 5)

	encoder, _ = sds.Add(ctx, "fooVideo3")
	encodeVideo(encoder, 15)

	if ok, err := sds.Remove(ctx, "fooVideo2"); err != nil {
		t.Errorf("Failed Remove, details: %v", err)
	} else if !ok {
		t.Error("Failed Remove, got false, want true")
	}

	if sds.Count() != 65 {
		t.Errorf("Failed Remove, got %d want %d", sds.Count(), 65)
	}

	trans.Commit(ctx)
}

func encodeVideo(encoder *sd.Encoder[string], count int) {
	for i := 0; i < count; i++ {
		encoder.Encode("#%d. A huge chunk, about 20MB.")
	}
}
