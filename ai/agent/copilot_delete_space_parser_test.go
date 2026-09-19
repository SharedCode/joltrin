package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai"
)

func TestParseDeleteSpaceRequest_AcceptsReversedSpacePhrase(t *testing.T) {
	name, ok := parseDeleteSpaceRequest("delete task3 space")
	if !ok {
		t.Fatal("expected parser to match reversed delete space phrasing")
	}
	if name != "task3" {
		t.Fatalf("expected task3, got %q", name)
	}
}

func TestParseDeleteSpaceRequest_AcceptsReversedKnowledgeBasePhrase(t *testing.T) {
	name, ok := parseDeleteSpaceRequest("remove archive knowledge base")
	if !ok {
		t.Fatal("expected parser to match reversed knowledge base phrasing")
	}
	if name != "archive" {
		t.Fatalf("expected archive, got %q", name)
	}
}

func TestParseDeleteSpaceRequest_AcceptsCurrentDatabasePrefixedReversedSpacePhrase(t *testing.T) {
	name, ok := parseDeleteSpaceRequest("Current Database: dev_db\ndelete task3 space")
	if !ok {
		t.Fatal("expected parser to match prefixed reversed delete space phrasing")
	}
	if name != "task3" {
		t.Fatalf("expected task3, got %q", name)
	}
}

func TestHandlePendingUserConfirmation_CatchesPrefixedReversedDeletePhrase(t *testing.T) {
	svc := &Service{session: &RunnerSession{}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})

	handled, msg, err := svc.handlePendingUserConfirmation(ctx, "Current Database: dev_db\ndelete task3 space")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handled {
		t.Fatal("expected delete confirmation flow to intercept reversed delete phrase")
	}
	if !strings.Contains(msg, `Delete Space "task3" from database "dev_db"?`) {
		t.Fatalf("unexpected confirmation prompt: %q", msg)
	}
	if svc.session.PendingConfirmation == nil || svc.session.PendingConfirmation.SpaceName != "task3" {
		t.Fatalf("expected pending confirmation state for task3, got %#v", svc.session.PendingConfirmation)
	}
}

func TestIsNegativeConfirmation_AcceptsCurrentDatabasePrefixedReply(t *testing.T) {
	if !isNegativeConfirmation("Current Database: dev_db\nno") {
		t.Fatal("expected prefixed negative confirmation to match")
	}
}

func TestIsAffirmativeConfirmation_AcceptsCurrentDatabasePrefixedReply(t *testing.T) {
	if !isAffirmativeConfirmation("Current Database: dev_db\nyes") {
		t.Fatal("expected prefixed affirmative confirmation to match")
	}
}
