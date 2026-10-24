package intent

import (
	"crypto/ed25519"
	"sync"
	"testing"
	"time"
)

type lockFixture struct {
	now   time.Time
	owner ed25519.PrivateKey
	pub   ed25519.PublicKey
}

func newLockFixture(t *testing.T) *lockFixture {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return &lockFixture{now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), owner: priv, pub: pub}
}

func (f *lockFixture) lock(t *testing.T, c Contract) *ScopeLock {
	t.Helper()
	c = SignContract(f.owner, c, f.now, time.Hour)
	l, err := NewScopeLock(c, f.pub, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func reviewContract(agent string) Contract {
	return Contract{
		Tenant: "acme", Agent: agent, Task: "review PR 412",
		Tools:      []string{"pr.read", "pr.comment"},
		Resources:  []string{"acme/api/pull/412/*"},
		MaxActions: 3,
	}
}

func prAction(agent, tool, res string) Action {
	return Action{Tenant: "acme", Agent: agent, Tool: tool, Resource: res, Env: "ci"}
}

func TestScopeLockBlocksDrift(t *testing.T) {
	f := newLockFixture(t)
	l := f.lock(t, reviewContract("rev-1"))

	cases := []struct {
		name string
		a    Action
		want Verdict
		code string
	}{
		{"read a changed file", prAction("rev-1", "pr.read", "acme/api/pull/412/main.go"), Allow, ""},
		{"edit code nobody asked for", prAction("rev-1", "repo.write", "acme/api/pull/412/main.go"), Block, CodeToolOutOfScope},
		{"wander into another PR", prAction("rev-1", "pr.read", "acme/api/pull/399/main.go"), Block, CodeResourceOutOfScope},
		{"wander into another repo", prAction("rev-1", "pr.read", "acme/billing/pull/1/x.go"), Block, CodeResourceOutOfScope},
		{"someone else's contract", prAction("rev-2", "pr.read", "acme/api/pull/412/main.go"), Block, CodeWrongAgent},
	}
	for _, c := range cases {
		d := l.Check(c.a)
		if d.Verdict != c.want {
			t.Fatalf("%s: got %s, want %s (%s)", c.name, d.Verdict, c.want, d.Summary)
		}
		if c.code != "" && d.Reasons[0].Code != c.code {
			t.Fatalf("%s: code %s, want %s", c.name, d.Reasons[0].Code, c.code)
		}
	}
	r := l.Report()
	if r.Allowed != 1 || r.Blocked != 4 || !r.Drifted() {
		t.Fatalf("report: %+v", r)
	}
}

func TestScopeLockBudgetAndExpiry(t *testing.T) {
	f := newLockFixture(t)
	l := f.lock(t, reviewContract("rev-1"))
	a := prAction("rev-1", "pr.read", "acme/api/pull/412/a.go")
	for i := 0; i < 3; i++ {
		if d := l.Check(a); d.Verdict != Allow {
			t.Fatalf("call %d: %s", i, d.Summary)
		}
	}
	if d := l.Check(a); d.Verdict != Block || d.Reasons[0].Code != CodeBudget {
		t.Fatalf("fourth call should hit the budget: %+v", d)
	}
	f.now = f.now.Add(2 * time.Hour)
	l2 := f.lock(t, reviewContract("rev-1"))
	f.now = f.now.Add(2 * time.Hour)
	if d := l2.Check(a); d.Reasons[0].Code != CodeExpired {
		t.Fatalf("want expired, got %+v", d)
	}
}

func TestBlockedActionsDoNotSpendBudget(t *testing.T) {
	f := newLockFixture(t)
	l := f.lock(t, reviewContract("rev-1"))
	for i := 0; i < 10; i++ {
		l.Check(prAction("rev-1", "repo.write", "acme/api/pull/412/a.go"))
	}
	if d := l.Check(prAction("rev-1", "pr.read", "acme/api/pull/412/a.go")); d.Verdict != Allow {
		t.Fatalf("blocked attempts must not use the budget: %s", d.Summary)
	}
}

func TestContractMustBeSignedByOwner(t *testing.T) {
	f := newLockFixture(t)
	c := SignContract(f.owner, reviewContract("rev-1"), f.now, time.Hour)

	_, otherPriv, _ := GenerateKey()
	forged := SignContract(otherPriv, reviewContract("rev-1"), f.now, time.Hour)
	if _, err := NewScopeLock(forged, f.pub, nil); err == nil {
		t.Fatal("a contract signed by another key must be refused")
	}

	widened := c
	widened.Resources = []string{"*/*"}
	if _, err := NewScopeLock(widened, f.pub, nil); err == nil {
		t.Fatal("an edited contract must be refused")
	}
}

func TestClaimCheck(t *testing.T) {
	f := newLockFixture(t)
	l := f.lock(t, reviewContract("rev-1"))
	src := MapSource{
		"acme/api/pull/412/main.go": "func Save(u *User) {\n\tdb.Exec(\"UPDATE users SET name=\" + u.Name)\n}\n",
	}
	cases := []struct {
		name string
		c    Claim
		ok   bool
		code string
	}{
		{"real quote", Claim{"SQL built by string concatenation", []Citation{{"acme/api/pull/412/main.go", `db.Exec("UPDATE users SET name=" + u.Name)`}}}, true, ""},
		{"whitespace differences are fine", Claim{"same", []Citation{{"acme/api/pull/412/main.go", "func Save(u *User) {   db.Exec("}}}, true, ""},
		{"no citation", Claim{"there is a race condition", nil}, false, CodeNoEvidence},
		{"invented code", Claim{"missing nil check", []Citation{{"acme/api/pull/412/main.go", "if u == nil { return err }"}}}, false, CodeQuoteNotFound},
		{"one word quote", Claim{"bad", []Citation{{"acme/api/pull/412/main.go", "func"}}}, false, CodeWeakQuote},
		{"cites a file it was never given", Claim{"leak", []Citation{{"acme/billing/pull/1/keys.go", "const apiKey = secret"}}}, false, CodeCitedOutOfScope},
		{"cites a file that does not exist", Claim{"ghost", []Citation{{"acme/api/pull/412/ghost.go", "func Ghost() error {"}}}, false, CodeSourceMissing},
		{"one real and one invented cite", Claim{"mixed", []Citation{
			{"acme/api/pull/412/main.go", "func Save(u *User) {"},
			{"acme/api/pull/412/main.go", "defer tx.Rollback()"},
		}}, false, CodeQuoteNotFound},
	}
	for _, c := range cases {
		got := l.CheckClaim(c.c, src)
		if got.Grounded != c.ok || got.Code != c.code {
			t.Fatalf("%s: got %+v", c.name, got)
		}
	}
}

func TestSwarmSummary(t *testing.T) {
	f := newLockFixture(t)
	src := MapSource{"acme/api/pull/412/main.go": "package main // the whole file text lives here"}
	var reports []Report
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range []string{"rev-a", "rev-b", "rev-c"} {
		l := f.lock(t, reviewContract(name))
		wg.Add(1)
		go func(name string, l *ScopeLock) {
			defer wg.Done()
			l.Check(prAction(name, "pr.read", "acme/api/pull/412/main.go"))
			if name == "rev-b" {
				l.Check(prAction(name, "repo.write", "acme/api/pull/412/main.go"))
			}
			cite := "the whole file text lives here"
			if name == "rev-c" {
				cite = "a line that was never in the file"
			}
			l.CheckClaim(Claim{"finding", []Citation{{"acme/api/pull/412/main.go", cite}}}, src)
			mu.Lock()
			reports = append(reports, l.Report())
			mu.Unlock()
		}(name, l)
	}
	wg.Wait()
	s := Summarize(reports...)
	if s.Agents != 3 || s.Blocked != 1 || s.ClaimsGrounded != 2 || s.ClaimsRejected != 1 {
		t.Fatalf("summary: %+v", s)
	}
	if len(s.Drifted) != 2 || s.Drifted[0] != "rev-b" || s.Drifted[1] != "rev-c" {
		t.Fatalf("drifted: %v", s.Drifted)
	}
}
