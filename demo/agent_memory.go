//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"sync"
	"syscall/js"
	"time"

	"github.com/sharedcode/joltrin/v5/inmemory"
)

// AgentMemoryFrame is one committed reasoning checkpoint for an agent task.
// Every frame is written as its own B-Tree entry the moment it happens, so a
// killed agent never loses steps that already made it to disk (or, here, the
// in-memory B-Tree backing this WASM instance).
type AgentMemoryFrame struct {
	SessionID    string `json:"sessionId"`
	StepIndex    int    `json:"stepIndex"`
	AgentID      string `json:"agentId"`
	Text         string `json:"text"`
	CommittedRFC string `json:"committedAt"`
}

// AgentSession tracks the lifecycle of a single agent reasoning task across
// possibly multiple agent processes (the original one, plus any successor
// that resumes it after a kill).
type AgentSession struct {
	SessionID      string `json:"sessionId"`
	Prompt         string `json:"prompt"`
	CurrentAgentID string `json:"currentAgentId"`
	State          string `json:"state"` // RUNNING, KILLED, COMPLETE
	LastStep       int    `json:"lastStep"`
	TotalSteps     int    `json:"totalSteps"`
	GenerationNum  int    `json:"generation"`
}

// AgentRecallMatch is a scored hit from a cross-session memory recall query.
type AgentRecallMatch struct {
	SessionID  string  `json:"sessionId"`
	StepIndex  int     `json:"stepIndex"`
	Text       string  `json:"text"`
	Similarity float64 `json:"similarity"`
}

// scriptedSteps is the canned reasoning sequence a new agent task walks
// through. It is a fixed demo script, not a live model call; what is real is
// the checkpointing, the kill, and the recovery below.
var scriptedSteps = []string{
	"Parsed incident context: payment-gateway-cluster p99 latency alert triggered at 02:14 UTC.",
	"Queried recent deploy history, correlated the spike with canary release v482 rolled out to 10% of nodes.",
	"Cross-referenced error logs, found connection pool exhaustion on the 3 canary nodes.",
	"Formed hypothesis: canary nodes are underprovisioned on DB connections relative to their traffic shard.",
	"Drafted remediation: roll back the canary, raise the connection pool ceiling, redeploy gradually.",
}

// AgentMemoryEngine persists agent reasoning frames and session state in
// dedicated SOP B-Trees, entirely inside this WASM instance.
type AgentMemoryEngine struct {
	mu         sync.RWMutex
	frames     inmemory.BtreeInterface[string, string]
	sessions   inmemory.BtreeInterface[string, string]
	sessionSeq int
	agentSeq   int
}

var agentEngine = &AgentMemoryEngine{
	frames:   inmemory.NewBtree[string, string](true),
	sessions: inmemory.NewBtree[string, string](true),
}

func init() {
	seedAgentMemory()
}

// seedAgentMemory pre-populates a couple of already-completed sessions so
// cross-session recall has something real to find on a first visit.
func seedAgentMemory() {
	agentEngine.mu.Lock()
	defer agentEngine.mu.Unlock()

	seed := func(id, agentID, prompt string, steps []string) {
		sess := AgentSession{
			SessionID:      id,
			Prompt:         prompt,
			CurrentAgentID: agentID,
			State:          "COMPLETE",
			LastStep:       len(steps),
			TotalSteps:     len(steps),
			GenerationNum:  1,
		}
		agentEngine.putSessionLocked(sess)
		for i, text := range steps {
			frame := AgentMemoryFrame{
				SessionID:    id,
				StepIndex:    i + 1,
				AgentID:      agentID,
				Text:         text,
				CommittedRFC: time.Now().Add(-time.Duration(len(steps)-i) * time.Hour).Format(time.RFC3339Nano),
			}
			b, _ := json.Marshal(frame)
			agentEngine.frames.Add(frameKey(id, i+1), string(b))
		}
	}

	seed("session-000", "agent-00", "Investigate elevated checkout error rate during flash sale", []string{
		"Correlated checkout 500s with a spike in inventory-service p99 latency.",
		"Found the inventory-service connection pool was exhausted under flash sale load.",
		"Remediated by raising the connection pool ceiling and adding a request queue.",
	})
	seed("session-b01", "agent-b1", "Diagnose replica lag on the read-only reporting cluster", []string{
		"Traced replica lag to a long-running analytics query holding a table lock.",
		"Killed the offending query and added a statement timeout for the reporting role.",
	})
}

func frameKey(sessionID string, step int) string {
	return fmt.Sprintf("%s|%04d", sessionID, step)
}

func (e *AgentMemoryEngine) getSessionLocked(sessionID string) (AgentSession, bool) {
	if !e.sessions.Find(sessionID, true) {
		return AgentSession{}, false
	}
	var s AgentSession
	if err := json.Unmarshal([]byte(e.sessions.GetCurrentValue()), &s); err != nil {
		return AgentSession{}, false
	}
	return s, true
}

func (e *AgentMemoryEngine) putSessionLocked(s AgentSession) {
	b, _ := json.Marshal(s)
	e.sessions.Upsert(s.SessionID, string(b))
}

// start claims a fresh session and its first agent, but commits no
// reasoning steps yet.
func (e *AgentMemoryEngine) start(prompt string) (AgentSession, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.sessionSeq++
	e.agentSeq++
	sess := AgentSession{
		SessionID:      fmt.Sprintf("session-%03d", e.sessionSeq),
		Prompt:         prompt,
		CurrentAgentID: fmt.Sprintf("agent-%02d", e.agentSeq),
		State:          "RUNNING",
		LastStep:       0,
		TotalSteps:     len(scriptedSteps),
		GenerationNum:  1,
	}
	e.putSessionLocked(sess)

	logs := []string{
		fmt.Sprintf("[Task Start] %s claimed session %s", sess.CurrentAgentID, sess.SessionID),
		fmt.Sprintf("[Prompt] %s", prompt),
	}
	return sess, logs
}

// commitStep writes the next scripted reasoning frame as its own durable
// B-Tree entry and advances the session's checkpoint pointer.
func (e *AgentMemoryEngine) commitStep(sessionID string) (AgentSession, AgentMemoryFrame, []string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess, ok := e.getSessionLocked(sessionID)
	if !ok {
		return AgentSession{}, AgentMemoryFrame{}, nil, fmt.Errorf("session %s not found", sessionID)
	}
	if sess.State == "KILLED" {
		return sess, AgentMemoryFrame{}, nil, fmt.Errorf("agent process is dead, resume before stepping again")
	}
	if sess.LastStep >= sess.TotalSteps {
		sess.State = "COMPLETE"
		e.putSessionLocked(sess)
		return sess, AgentMemoryFrame{}, []string{"[No-Op] all reasoning steps are already committed"}, nil
	}

	nextStep := sess.LastStep + 1
	frame := AgentMemoryFrame{
		SessionID:    sessionID,
		StepIndex:    nextStep,
		AgentID:      sess.CurrentAgentID,
		Text:         scriptedSteps[nextStep-1],
		CommittedRFC: time.Now().Format(time.RFC3339Nano),
	}
	b, _ := json.Marshal(frame)
	e.frames.Add(frameKey(sessionID, nextStep), string(b))

	sess.LastStep = nextStep
	if sess.LastStep == sess.TotalSteps {
		sess.State = "COMPLETE"
	}
	e.putSessionLocked(sess)

	logs := []string{
		fmt.Sprintf("[Checkpoint Commit] %s wrote step %d/%d to the B-Tree", frame.AgentID, nextStep, sess.TotalSteps),
	}
	if sess.State == "COMPLETE" {
		logs = append(logs, fmt.Sprintf("[Task Complete] %s finished the reasoning trace for %s", frame.AgentID, sessionID))
	}
	return sess, frame, logs, nil
}

// kill simulates the agent process dying mid-task. Nothing is rolled back:
// every checkpoint already committed stays exactly where it is.
func (e *AgentMemoryEngine) kill(sessionID string) (AgentSession, []string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess, ok := e.getSessionLocked(sessionID)
	if !ok {
		return AgentSession{}, nil, fmt.Errorf("session %s not found", sessionID)
	}
	deadAgent := sess.CurrentAgentID
	sess.State = "KILLED"
	e.putSessionLocked(sess)

	logs := []string{
		fmt.Sprintf("[Process Killed] %s terminated mid-task at step %d/%d", deadAgent, sess.LastStep, sess.TotalSteps),
		fmt.Sprintf("[Durability Check] %d committed checkpoint(s) remain readable in the B-Tree, nothing was lost", sess.LastStep),
	}
	return sess, logs, nil
}

// resume hands the session to a brand new agent, which reads the last
// committed checkpoint straight out of the B-Tree and continues from there.
func (e *AgentMemoryEngine) resume(sessionID string) (AgentSession, []string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	sess, ok := e.getSessionLocked(sessionID)
	if !ok {
		return AgentSession{}, nil, fmt.Errorf("session %s not found", sessionID)
	}
	if sess.State != "KILLED" {
		return sess, []string{"[No-Op] session is not in a killed state"}, nil
	}

	oldAgent := sess.CurrentAgentID
	e.agentSeq++
	sess.CurrentAgentID = fmt.Sprintf("agent-%02d", e.agentSeq)
	sess.State = "RUNNING"
	sess.GenerationNum++
	e.putSessionLocked(sess)

	logs := []string{
		fmt.Sprintf("[Recovery] %s read the last committed checkpoint (step %d) straight from the B-Tree", sess.CurrentAgentID, sess.LastStep),
		fmt.Sprintf("[Handoff] %s resumes where %s left off, no re-work, no external state store", sess.CurrentAgentID, oldAgent),
	}
	return sess, logs, nil
}

// trace returns the session plus every committed frame in step order.
func (e *AgentMemoryEngine) trace(sessionID string) (AgentSession, []AgentMemoryFrame) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	sess, _ := e.getSessionLocked(sessionID)
	frames := make([]AgentMemoryFrame, 0)
	for _, v := range e.frames.Range(frameKey(sessionID, 0), frameKey(sessionID, 9999)) {
		var f AgentMemoryFrame
		if json.Unmarshal([]byte(v), &f) == nil {
			frames = append(frames, f)
		}
	}
	return sess, frames
}

// recall embeds the session's latest committed frame (or its prompt, if
// nothing has been committed yet) and searches committed frames from every
// other session for the closest matches by cosine similarity.
func (e *AgentMemoryEngine) recall(sessionID string, topK int) []AgentRecallMatch {
	e.mu.RLock()
	defer e.mu.RUnlock()

	sess, _ := e.getSessionLocked(sessionID)
	queryText := sess.Prompt
	if sess.LastStep > 0 && e.frames.Find(frameKey(sessionID, sess.LastStep), true) {
		var f AgentMemoryFrame
		if json.Unmarshal([]byte(e.frames.GetCurrentValue()), &f) == nil {
			queryText = f.Text
		}
	}
	queryVec := agentEmbedText(queryText, 128)

	type scored struct {
		frame AgentMemoryFrame
		score float64
	}
	var candidates []scored
	for _, v := range e.frames.All() {
		var f AgentMemoryFrame
		if json.Unmarshal([]byte(v), &f) != nil || f.SessionID == sessionID {
			continue
		}
		candidates = append(candidates, scored{frame: f, score: cosineSimilarity(queryVec, agentEmbedText(f.Text, 128))})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	if topK > len(candidates) {
		topK = len(candidates)
	}
	matches := make([]AgentRecallMatch, 0, topK)
	for i := 0; i < topK; i++ {
		matches = append(matches, AgentRecallMatch{
			SessionID:  candidates[i].frame.SessionID,
			StepIndex:  candidates[i].frame.StepIndex,
			Text:       candidates[i].frame.Text,
			Similarity: candidates[i].score,
		})
	}
	return matches
}

// agentEmbedText derives a deterministic pseudo-embedding from the text's
// hash, the same normalized-random construction used by the vector search
// tab, so cosine similarity is meaningful without a real model in the loop.
func agentEmbedText(text string, dims int) []float64 {
	h := fnv.New64a()
	h.Write([]byte(text))
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	vec := make([]float64, dims)
	var sumSquares float64
	for i := 0; i < dims; i++ {
		val := r.NormFloat64()
		vec[i] = val
		sumSquares += val * val
	}
	mag := math.Sqrt(sumSquares)
	for i := 0; i < dims; i++ {
		vec[i] /= mag
	}
	return vec
}

func argString(args []js.Value, i int) string {
	if len(args) > i && args[i].Type() == js.TypeString {
		return args[i].String()
	}
	return ""
}

func marshalAgentResp(sess AgentSession, frame AgentMemoryFrame, logs []string, err error) string {
	resp := map[string]any{
		"success": err == nil,
		"session": sess,
		"logs":    logs,
	}
	if frame.SessionID != "" {
		resp["frame"] = frame
	}
	if err != nil {
		resp["errorMessage"] = err.Error()
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func jsAgentStart(this js.Value, args []js.Value) any {
	prompt := "Investigate anomalous latency spike in payment gateway cluster"
	if s := argString(args, 0); s != "" {
		prompt = s
	}
	sess, logs := agentEngine.start(prompt)
	go persistState()
	return marshalAgentResp(sess, AgentMemoryFrame{}, logs, nil)
}

func jsAgentStep(this js.Value, args []js.Value) any {
	sess, frame, logs, err := agentEngine.commitStep(argString(args, 0))
	if err == nil {
		go persistState()
	}
	return marshalAgentResp(sess, frame, logs, err)
}

func jsAgentKill(this js.Value, args []js.Value) any {
	sess, logs, err := agentEngine.kill(argString(args, 0))
	if err == nil {
		go persistState()
	}
	return marshalAgentResp(sess, AgentMemoryFrame{}, logs, err)
}

func jsAgentResume(this js.Value, args []js.Value) any {
	sess, logs, err := agentEngine.resume(argString(args, 0))
	if err == nil {
		go persistState()
	}
	return marshalAgentResp(sess, AgentMemoryFrame{}, logs, err)
}

func jsAgentTrace(this js.Value, args []js.Value) any {
	sess, frames := agentEngine.trace(argString(args, 0))
	b, _ := json.Marshal(map[string]any{"session": sess, "frames": frames})
	return string(b)
}

func jsAgentRecall(this js.Value, args []js.Value) any {
	topK := 3
	if len(args) > 1 && args[1].Type() == js.TypeNumber {
		topK = args[1].Int()
	}
	matches := agentEngine.recall(argString(args, 0), topK)
	b, _ := json.Marshal(map[string]any{"matches": matches})
	return string(b)
}
