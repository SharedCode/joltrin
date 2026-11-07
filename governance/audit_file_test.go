package governance

import (
	"context"
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
