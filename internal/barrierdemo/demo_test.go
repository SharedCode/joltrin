package barrierdemo

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

const expectedOutput = `✓ workflow registered: rollback_complete is reachable from every state

agent: "backup looks fine, dropping prod now"
→ execute_step(drop_prod_db) ... BLOCKED
  barrier certificate: step "drop_prod_db" requires state "backup_validated", which has not been established in this trace

agent: okay, taking a real backup first
→ execute_step(take_backup) ... committed
→ execute_step(validate_backup) ... committed

agent: backup is now validated, retrying the drop
→ execute_step(drop_prod_db) ... ALLOWED, committed

trace: [take_backup validate_backup drop_prod_db]
`

func TestRunPreservesBlockedAndAllowedTrace(t *testing.T) {
	var out bytes.Buffer
	if err := Run(&out, 0); err != nil {
		t.Fatal(err)
	}
	if out.String() != expectedOutput {
		t.Fatalf("unexpected transcript:\n%s", out.String())
	}
}

type failingWriter struct {
	err    error
	writes int
	short  bool
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.err
}

func TestRunReportsOutputFailure(t *testing.T) {
	failure := errors.New("output unavailable")
	for _, tc := range []struct {
		name   string
		writer *failingWriter
		want   error
	}{
		{"write error", &failingWriter{err: failure}, failure},
		{"short write", &failingWriter{short: true}, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Run(tc.writer, 0); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.writer.writes != 1 {
				t.Fatalf("continued writing after failure: %d writes", tc.writer.writes)
			}
		})
	}
	if err := Run(nil, 0); err == nil {
		t.Fatal("accepted a nil output writer")
	}
}

func TestPausePreservesEnvironmentBehavior(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 150 * time.Millisecond}, {"invalid", 150 * time.Millisecond}, {"0", 0},
		{"0s", 0}, {"700ms", 700 * time.Millisecond}, {"-1s", -time.Second},
	} {
		if got := Pause(tc.value); got != tc.want {
			t.Errorf("Pause(%q)=%s, want %s", tc.value, got, tc.want)
		}
	}
}
