package governance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileEv(action string) *AuditEvent {
	return &AuditEvent{ActorID: "a", Action: action, Resource: "r", Decision: DecisionAllow, Metadata: map[string]string{"k": "v"}}
}

func TestFileAuditRecorderSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	signer, pub, _ := GenerateEd25519Signer()
	ctx := context.Background()

	r, err := OpenFileAuditRecorder(path, signer)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"one", "two"} {
		if err := r.Record(ctx, fileEv(a)); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()

	r2, err := OpenFileAuditRecorder(path, signer)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if r2.Count() != 2 {
		t.Fatalf("replayed %d events, want 2", r2.Count())
	}
	if err := r2.Record(ctx, fileEv("three")); err != nil {
		t.Fatal(err)
	}
	if err := r2.VerifySignatures(pub); err != nil {
		t.Fatalf("chain across restart: %v", err)
	}

	r3, err := OpenFileAuditRecorder(path, signer)
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Close()
	if r3.Count() != 3 {
		t.Fatalf("got %d events, want 3", r3.Count())
	}
}

func TestFileAuditRecorderRefusesTamperedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r, _ := OpenFileAuditRecorder(path, nil)
	r.Record(context.Background(), fileEv("one"))
	r.Close()

	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), `"v"`, `"x"`, 1)), 0o600)
	if _, err := OpenFileAuditRecorder(path, nil); err == nil {
		t.Fatal("tampered metadata was accepted")
	}
}

func TestFileAuditRecorderRefusesTruncatedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r, _ := OpenFileAuditRecorder(path, nil)
	r.Record(context.Background(), fileEv("one"))
	r.Close()

	b, _ := os.ReadFile(path)
	os.WriteFile(path, b[:len(b)-20], 0o600)
	if _, err := OpenFileAuditRecorder(path, nil); err == nil {
		t.Fatal("half-written line was accepted")
	}
}

func TestFileAuditRecorderFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r, err := OpenFileAuditRecorder(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestFileAuditRecorderDeletedLineBreaksChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r, _ := OpenFileAuditRecorder(path, nil)
	for _, a := range []string{"one", "two", "three"} {
		r.Record(context.Background(), fileEv(a))
	}
	r.Close()
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	os.WriteFile(path, []byte(lines[0]+"\n"+lines[2]+"\n"), 0o600)
	if _, err := OpenFileAuditRecorder(path, nil); err == nil {
		t.Fatal("removed middle event was accepted")
	}
}

func TestFileAuditRecorderAnchorDetectsTruncatedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r, _ := OpenFileAuditRecorder(path, nil)
	for _, a := range []string{"one", "two", "three"} {
		r.Record(context.Background(), fileEv(a))
	}
	anchor := r.LastHash()
	r.Close()

	if r2, err := OpenFileAuditRecorderAnchored(path, nil, anchor); err != nil {
		t.Fatalf("intact log with anchor: %v", err)
	} else {
		r2.Close()
	}

	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	os.WriteFile(path, []byte(strings.Join(lines[:2], "\n")+"\n"), 0o600)
	if _, err := OpenFileAuditRecorderAnchored(path, nil, anchor); !errors.Is(err, ErrAuditTruncated) {
		t.Fatalf("got %v, want ErrAuditTruncated", err)
	}
	if r3, err := OpenFileAuditRecorder(path, nil); err != nil {
		t.Fatalf("without an anchor the shorter chain opens: %v", err)
	} else {
		r3.Close()
	}
}

func TestFileAuditRecorderRefusesNonRegularFile(t *testing.T) {
	if _, err := OpenFileAuditRecorder(t.TempDir(), nil); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := OpenFileAuditRecorder(os.DevNull, nil); err == nil {
		t.Fatal("/dev/null accepted")
	}
}

func TestFileAuditRecorderRollsBackWhenTheWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	r, err := OpenFileAuditRecorder(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.Record(ctx, fileEv("one")); err != nil {
		t.Fatal(err)
	}
	before := r.LastHash()

	r.file.Close() // every later write now fails
	if err := r.Record(ctx, fileEv("two")); err == nil {
		t.Fatal("write to a closed file reported success")
	}
	if r.Count() != 1 {
		t.Fatalf("memory holds %d events, want 1 (the failed event must be removed)", r.Count())
	}
	if r.LastHash() != before {
		t.Fatal("last hash moved although the event was not stored")
	}
	if err := r.VerifyIntegrity(); err != nil {
		t.Fatalf("chain after a failed write: %v", err)
	}

	r2, err := OpenFileAuditRecorder(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if r2.Count() != 1 {
		t.Fatalf("file holds %d events, want 1", r2.Count())
	}
}

func TestFileAuditRecorderLeavesAForeignFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.log")
	original := "Nov  7 11:00:01 host sshd[42]: Accepted publickey for deploy\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileAuditRecorder(path, nil); err == nil {
		t.Fatal("a file that is not an audit chain was opened for writing")
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("file was modified: %q", got)
	}
}

func TestFileAuditRecorderRefusesDotDotInThePath(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(filepath.Separator) + "sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + "audit.jsonl"
	if _, err := OpenFileAuditRecorder(path, nil); err == nil {
		t.Fatal("path with .. was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.jsonl")); err == nil {
		t.Fatal("a file was created through the .. path")
	}
}
