package blocklog

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

func fill(b *testing.B, l *Log, runs int) {
	b.Helper()
	for i := 0; i < runs; i++ {
		if _, err := l.Record(Entry{
			Workflow: "db-maintenance", Version: "v1", TraceID: fmt.Sprintf("run-%d", i),
			Step: verify.StepID(fmt.Sprintf("step_%d", i%20)), BlockedBy: "precondition",
			MissingState: verify.State(fmt.Sprintf("state_%d", i%7)), At: time.Now(),
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSummaries is the lesson summary over a full in-memory log.
func BenchmarkSummaries(b *testing.B) {
	l := New()
	fill(b, l, DefaultMaxEntries)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Summaries("db-maintenance", "v1")
	}
}

// BenchmarkStats is the run and rule totals over the same log.
func BenchmarkStats(b *testing.B) {
	l := New()
	fill(b, l, DefaultMaxEntries/2)
	for i := 0; i < DefaultMaxEntries/2; i++ {
		_, _ = l.RecordRun("db-maintenance", "v1", fmt.Sprintf("run-%d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Stats("db-maintenance", "v1")
	}
}

// BenchmarkRecordToFile is one new block appended to the file.
func BenchmarkRecordToFile(b *testing.B) {
	l, err := Open(filepath.Join(b.TempDir(), "blocks.jsonl"), WithMaxEntries(1<<30))
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Record(Entry{
			Workflow: "db-maintenance", Version: "v1", TraceID: fmt.Sprintf("run-%d", i),
			Step: "drop_prod_db", BlockedBy: "precondition", MissingState: "backup_validated", At: time.Now(),
		}); err != nil {
			b.Fatal(err)
		}
	}
}
