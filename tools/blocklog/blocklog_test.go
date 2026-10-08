package blocklog

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

func entry(trace string, step verify.StepID) Entry {
	return Entry{
		Workflow:      "db-maintenance",
		Version:       "v1",
		TraceID:       trace,
		Step:          step,
		BlockedBy:     "precondition",
		MissingState:  "backup_validated",
		EstablishedBy: []verify.StepID{"validate_backup"},
	}
}

func TestRecordCountsOncePerRun(t *testing.T) {
	l := New()
	for i := 0; i < 5; i++ {
		added, err := l.Record(entry("run-1", "drop_prod_db"))
		if err != nil {
			t.Fatal(err)
		}
		if added != (i == 0) {
			t.Fatalf("call %d: added = %v", i, added)
		}
	}
	l.Record(entry("run-2", "drop_prod_db"))

	got := l.Summaries("db-maintenance", "v1")
	if len(got) != 1 || got[0].Runs != 2 {
		t.Fatalf("want one summary with 2 runs, got %+v", got)
	}
	if got[0].Step != "drop_prod_db" || got[0].MissingState != "backup_validated" {
		t.Errorf("unexpected summary: %+v", got[0])
	}
	if len(got[0].EstablishedBy) != 1 || got[0].EstablishedBy[0] != "validate_backup" {
		t.Errorf("EstablishedBy = %v", got[0].EstablishedBy)
	}
}

func TestSummariesIgnoreOtherWorkflowAndVersion(t *testing.T) {
	l := New()
	l.Record(entry("run-1", "drop_prod_db"))
	if got := l.Summaries("other", "v1"); len(got) != 0 {
		t.Errorf("other workflow: %+v", got)
	}
	if got := l.Summaries("db-maintenance", "v2"); len(got) != 0 {
		t.Errorf("a changed runbook must not reuse old blocks: %+v", got)
	}
}

func TestSummariesOrderMostFrequentFirst(t *testing.T) {
	l := New()
	l.Record(entry("a", "validate_backup"))
	l.Record(entry("a", "drop_prod_db"))
	l.Record(entry("b", "drop_prod_db"))
	got := l.Summaries("db-maintenance", "v1")
	if len(got) != 2 || got[0].Step != "drop_prod_db" || got[0].Runs != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestEntriesExpire(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	clock := now
	l := New(WithTTL(24*time.Hour), WithClock(func() time.Time { return clock }))
	l.Record(entry("old", "drop_prod_db"))
	clock = now.Add(48 * time.Hour)
	l.Record(entry("new", "drop_prod_db"))
	got := l.Summaries("db-maintenance", "v1")
	if len(got) != 1 || got[0].Runs != 1 {
		t.Fatalf("expired entry must not count: %+v", got)
	}
}

func TestMaxEntriesDropsOldest(t *testing.T) {
	l := New(WithMaxEntries(2))
	l.Record(entry("a", "drop_prod_db"))
	l.Record(entry("b", "drop_prod_db"))
	l.Record(entry("c", "drop_prod_db"))
	if got := l.Summaries("db-maintenance", "v1"); got[0].Runs != 2 {
		t.Fatalf("want 2 runs kept, got %+v", got)
	}
	// "a" was dropped, so the same run can be recorded again.
	if added, _ := l.Record(entry("a", "drop_prod_db")); !added {
		t.Error("an evicted run should be recordable again")
	}
}

func TestFilePersistsAcrossOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocks.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(entry("run-1", "drop_prod_db"))
	l.Record(entry("run-2", "drop_prod_db"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if got := l2.Summaries("db-maintenance", "v1"); len(got) != 1 || got[0].Runs != 2 {
		t.Fatalf("reopened log lost entries: %+v", got)
	}
	// The same run is still deduplicated after a restart.
	if added, _ := l2.Record(entry("run-1", "drop_prod_db")); added {
		t.Error("run-1 was already recorded before the restart")
	}
}

func TestOpenSkipsBadLinesAndCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocks.jsonl")
	good := `{"at":"` + time.Now().UTC().Format(time.RFC3339) + `","workflow":"db-maintenance","version":"v1","trace_id":"run-1","step":"drop_prod_db","blocked_by":"precondition","missing_state":"backup_validated"}`
	if err := os.WriteFile(path, []byte("not json\n"+good+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := l.Summaries("db-maintenance", "v1"); len(got) != 1 {
		t.Fatalf("good line should load: %+v", got)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "not json") {
		t.Errorf("file should be compacted without the bad line:\n%s", b)
	}
}

func TestOpenDropsExpiredEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocks.jsonl")
	old := time.Now().Add(-90 * 24 * time.Hour).UTC().Format(time.RFC3339)
	line := `{"at":"` + old + `","workflow":"w","version":"v1","trace_id":"t","step":"s","blocked_by":"precondition","missing_state":"m"}`
	os.WriteFile(path, []byte(line+"\n"), 0o600)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := l.Summaries("w", "v1"); len(got) != 0 {
		t.Errorf("expired entry loaded: %+v", got)
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Errorf("expired entry should be gone from the file, got %q", b)
	}
}

func TestRecordIsSafeForConcurrentUse(t *testing.T) {
	l := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.Record(entry(string(rune('a'+i%10)), "drop_prod_db"))
			l.Summaries("db-maintenance", "v1")
		}(i)
	}
	wg.Wait()
	if got := l.Summaries("db-maintenance", "v1"); len(got) != 1 || got[0].Runs != 10 {
		t.Fatalf("want 10 distinct runs, got %+v", got)
	}
}

func TestRecordSurvivesAnotherProcessCompactingTheFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not let a file be replaced while another process has it open, so there is no compaction to survive")
	}
	path := filepath.Join(t.TempDir(), "blocks.jsonl")
	old := time.Now().Add(-90 * 24 * time.Hour)
	stale := entry("stale", "drop_prod_db")
	stale.At = old
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seed.Record(stale)
	seed.Close()

	// A second server starts, finds the expired line, and replaces the file
	// while the first still holds it open.
	first, err := Open(path, WithClock(func() time.Time { return old }))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	fresh := entry("run-1", "drop_prod_db")
	fresh.At = time.Now()
	if _, err := first.Record(fresh); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.Summaries("db-maintenance", "v1")
	if len(got) != 1 || got[0].Runs != 1 {
		t.Fatalf("the first server's block was lost: %+v", got)
	}
}

func TestOpenSucceedsWhenTheFileCannotBeCompacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocks.jsonl")
	stale := entry("stale", "drop_prod_db")
	stale.At = time.Now().Add(-90 * 24 * time.Hour)
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seed.Record(stale)
	seed.Close()

	// A directory in the way makes the replacement fail on every platform.
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatalf("a failed compaction must not stop the server starting: %v", err)
	}
	defer l.Close()
	if got := l.Summaries("db-maintenance", "v1"); len(got) != 0 {
		t.Errorf("the expired entry still counts: %+v", got)
	}
	if l.CompactionError() == nil {
		t.Error("the failed compaction should be reported")
	}
}

// summaryByScan is Summary as it was before the index: every entry is checked.
func summaryByScan(l *Log, workflow, version string, step verify.StepID, blockedBy string, missing verify.State) (s Summary, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-l.ttl)
	for i := range l.entries {
		e := &l.entries[i]
		if e.Kind != "" || e.Workflow != workflow || e.Version != version || e.Step != step ||
			e.BlockedBy != blockedBy || e.MissingState != missing || e.At.Before(cutoff) {
			continue
		}
		if !ok {
			s = Summary{Step: e.Step, BlockedBy: e.BlockedBy, MissingState: e.MissingState}
			ok = true
		}
		s.Runs++
		if !e.At.Before(s.LastSeen) {
			s.LastSeen = e.At
			s.EstablishedBy = e.EstablishedBy
		}
	}
	return s, ok
}

// TestSummaryIndexAgreesWithAFullScan records blocks, runs and recoveries
// through trims and expiry, and checks every lookup against the scan.
func TestSummaryIndexAgreesWithAFullScan(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := New(WithMaxEntries(40), WithTTL(10*time.Hour), WithClock(func() time.Time { return now }))
	rng := rand.New(rand.NewSource(1))
	steps := []verify.StepID{"a", "b", "c"}
	states := []verify.State{"x", "y"}
	for i := 0; i < 600; i++ {
		now = now.Add(time.Duration(rng.Intn(40)) * time.Minute)
		switch rng.Intn(4) {
		case 0:
			_, _ = l.RecordRun("w", "v1", fmt.Sprintf("t%d", rng.Intn(30)))
		case 1:
			_, _ = l.RecordRecovery("w", "v1", fmt.Sprintf("t%d", rng.Intn(30)), steps[rng.Intn(3)])
		default:
			_, _ = l.Record(Entry{
				Workflow: "w", Version: fmt.Sprintf("v%d", 1+rng.Intn(2)), TraceID: fmt.Sprintf("t%d", rng.Intn(30)),
				Step: steps[rng.Intn(3)], BlockedBy: "precondition", MissingState: states[rng.Intn(2)],
				EstablishedBy: []verify.StepID{steps[rng.Intn(3)]},
			})
		}
		for _, v := range []string{"v1", "v2"} {
			for _, st := range steps {
				for _, ms := range states {
					got, gok := l.Summary("w", v, st, "precondition", ms)
					want, wok := summaryByScan(l, "w", v, st, "precondition", ms)
					if gok != wok || !reflect.DeepEqual(got, want) {
						t.Fatalf("step %d, %s/%s/%s: index %+v %v, scan %+v %v", i, v, st, ms, got, gok, want, wok)
					}
				}
			}
		}
	}
	// Nothing is left behind for entries that were trimmed away.
	listed := 0
	for _, seqs := range l.bySum {
		listed += len(seqs)
	}
	blocks := 0
	for _, e := range l.entries {
		if e.Kind == "" {
			blocks++
		}
	}
	if listed != blocks {
		t.Fatalf("index lists %d entries, log holds %d blocks", listed, blocks)
	}
}
