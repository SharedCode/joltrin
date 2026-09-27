package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestInitializeRequest_UsesAuthenticatedUserID(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/ai/chat", strings.NewReader(`{"message":"hi","user_id":"client-random"}`))
	req = req.WithContext(sop.ContextWithAuth(req.Context(), sop.AuthContext{UserID: "alice"}))

	chatReq, err := initializeRequest(nil, req)
	if err != nil {
		t.Fatalf("initializeRequest failed: %v", err)
	}
	if got := chatReq.UserID; got != "alice" {
		t.Fatalf("expected authenticated user ID to override request payload, got %q", got)
	}
}

func TestConstructPayload_PreservesRawUserMessage(t *testing.T) {
	oldConfig := config
	defer func() { config = oldConfig }()

	config = Config{Databases: []DatabaseConfig{{Name: "demo", Path: t.TempDir()}}}

	req := &aiChatRequest{
		Message:  "show me orders",
		Database: "demo",
	}

	ctx, payload, _, fullMessage := constructPayload(context.Background(), httptest.NewRecorder(), req, llmSettings{}, func(string, any) {}, nil)
	if ctx == nil || payload == nil {
		t.Fatal("expected constructPayload to return context and payload")
	}
	if got := strings.TrimSpace(fullMessage); got != req.Message {
		t.Fatalf("expected raw user message to stay untouched, got %q want %q", got, req.Message)
	}
}
