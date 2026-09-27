//go:build js && wasm

package main

import (
	"testing"

	"github.com/sharedcode/joltrin/v5/inmemory"
)

func newTestEngine() *AgentMemoryEngine {
	return &AgentMemoryEngine{
		frames:   inmemory.NewBtree[string, string](true),
		sessions: inmemory.NewBtree[string, string](true),
	}
}

func TestAgentStartCreatesRunningSession(t *testing.T) {
	e := newTestEngine()
	sess, logs := e.start("diagnose the thing")

	if sess.State != "RUNNING" {
		t.Fatalf("expected state RUNNING, got %s", sess.State)
	}
	if sess.LastStep != 0 {
		t.Fatalf("expected LastStep 0, got %d", sess.LastStep)
	}
	if sess.TotalSteps != len(scriptedSteps) {
		t.Fatalf("expected TotalSteps %d, got %d", len(scriptedSteps), sess.TotalSteps)
	}
	if len(logs) == 0 {
		t.Fatal("expected non-empty start logs")
	}
}

func TestCommitStepAdvancesAndCompletes(t *testing.T) {
	e := newTestEngine()
	sess, _ := e.start("diagnose the thing")

	for i := 1; i <= len(scriptedSteps); i++ {
		next, frame, _, err := e.commitStep(sess.SessionID)
		if err != nil {
			t.Fatalf("commitStep %d: unexpected error: %v", i, err)
		}
		if frame.StepIndex != i {
			t.Fatalf("expected frame step %d, got %d", i, frame.StepIndex)
		}
		if next.LastStep != i {
			t.Fatalf("expected LastStep %d, got %d", i, next.LastStep)
		}
		sess = next
	}

	if sess.State != "COMPLETE" {
		t.Fatalf("expected state COMPLETE after final step, got %s", sess.State)
	}

	// Stepping again once complete is a no-op, not an error.
	final, frame, _, err := e.commitStep(sess.SessionID)
	if err != nil {
		t.Fatalf("commitStep after complete: unexpected error: %v", err)
	}
	if frame.SessionID != "" {
		t.Fatalf("expected no new frame once complete, got %+v", frame)
	}
	if final.LastStep != len(scriptedSteps) {
		t.Fatalf("expected LastStep unchanged at %d, got %d", len(scriptedSteps), final.LastStep)
	}
}

func TestKillPreservesCommittedFrames(t *testing.T) {
	e := newTestEngine()
	sess, _ := e.start("diagnose the thing")
	e.commitStep(sess.SessionID)
	e.commitStep(sess.SessionID)

	killed, _, err := e.kill(sess.SessionID)
	if err != nil {
		t.Fatalf("kill: unexpected error: %v", err)
	}
	if killed.State != "KILLED" {
		t.Fatalf("expected state KILLED, got %s", killed.State)
	}

	_, frames := e.trace(sess.SessionID)
	if len(frames) != 2 {
		t.Fatalf("expected 2 committed frames to survive the kill, got %d", len(frames))
	}

	// Stepping a killed session must fail, not silently commit further work.
	if _, _, _, err := e.commitStep(sess.SessionID); err == nil {
		t.Fatal("expected commitStep on a killed session to error")
	}
}

func TestResumeHandsOffToNewAgentFromLastCheckpoint(t *testing.T) {
	e := newTestEngine()
	sess, _ := e.start("diagnose the thing")
	e.commitStep(sess.SessionID)
	e.commitStep(sess.SessionID)
	killed, _, _ := e.kill(sess.SessionID)
	firstAgent := killed.CurrentAgentID

	resumed, _, err := e.resume(sess.SessionID)
	if err != nil {
		t.Fatalf("resume: unexpected error: %v", err)
	}
	if resumed.State != "RUNNING" {
		t.Fatalf("expected state RUNNING after resume, got %s", resumed.State)
	}
	if resumed.CurrentAgentID == firstAgent {
		t.Fatalf("expected a new successor agent, got the same agent %s", firstAgent)
	}
	if resumed.LastStep != 2 {
		t.Fatalf("expected resume to preserve LastStep 2, got %d", resumed.LastStep)
	}

	// The successor must continue from the checkpoint, not restart it.
	_, frame, _, err := e.commitStep(sess.SessionID)
	if err != nil {
		t.Fatalf("commitStep after resume: unexpected error: %v", err)
	}
	if frame.StepIndex != 3 {
		t.Fatalf("expected next committed step 3, got %d", frame.StepIndex)
	}
	if frame.AgentID != resumed.CurrentAgentID {
		t.Fatalf("expected frame committed by successor %s, got %s", resumed.CurrentAgentID, frame.AgentID)
	}
}

func TestResumeOnNonKilledSessionIsNoOp(t *testing.T) {
	e := newTestEngine()
	sess, _ := e.start("diagnose the thing")

	same, logs, err := e.resume(sess.SessionID)
	if err != nil {
		t.Fatalf("resume: unexpected error: %v", err)
	}
	if same.CurrentAgentID != sess.CurrentAgentID {
		t.Fatalf("expected agent unchanged for a non-killed resume, got %s vs %s", same.CurrentAgentID, sess.CurrentAgentID)
	}
	if len(logs) == 0 {
		t.Fatal("expected a no-op log entry")
	}
}

func TestRecallExcludesOwnSessionAndRanksBySimilarity(t *testing.T) {
	e := newTestEngine()
	a, _ := e.start("investigate a latency spike")
	e.commitStep(a.SessionID)
	e.commitStep(a.SessionID)

	b, _ := e.start("investigate a latency spike")
	e.commitStep(b.SessionID)
	e.commitStep(b.SessionID)

	matches := e.recall(a.SessionID, 5)
	if len(matches) == 0 {
		t.Fatal("expected at least one cross-session match")
	}
	for _, m := range matches {
		if m.SessionID == a.SessionID {
			t.Fatalf("recall must exclude the querying session's own frames, got a match from %s", m.SessionID)
		}
	}
	for i := 1; i < len(matches); i++ {
		if matches[i].Similarity > matches[i-1].Similarity {
			t.Fatalf("expected matches sorted by descending similarity, got %v then %v", matches[i-1].Similarity, matches[i].Similarity)
		}
	}
}

func TestFrameKeyOrderingMatchesStepOrder(t *testing.T) {
	if frameKey("s", 1) >= frameKey("s", 2) {
		t.Fatalf("expected frameKey(1) < frameKey(2), got %q >= %q", frameKey("s", 1), frameKey("s", 2))
	}
	if frameKey("s", 9) >= frameKey("s", 10) {
		t.Fatalf("expected zero-padded frameKey(9) < frameKey(10), got %q >= %q", frameKey("s", 9), frameKey("s", 10))
	}
}

func TestEmbedTextIsDeterministicAndUnitLength(t *testing.T) {
	v1 := agentEmbedText("some reasoning text", 128)
	v2 := agentEmbedText("some reasoning text", 128)
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("expected deterministic embedding, dim %d differed: %v vs %v", i, v1[i], v2[i])
		}
	}

	var sumSquares float64
	for _, x := range v1 {
		sumSquares += x * x
	}
	if sumSquares < 0.99 || sumSquares > 1.01 {
		t.Fatalf("expected roughly unit-length embedding, got squared magnitude %v", sumSquares)
	}
}
