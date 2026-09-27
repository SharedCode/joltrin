package main

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestStoreRBAC_VisibilityRules(t *testing.T) {
	cases := []struct {
		name    string
		ctx     context.Context
		store   string
		exposed bool
	}{
		{name: "plain store visible", ctx: sop.ContextWithAuth(context.Background(), sop.AuthContext{UserID: "alice"}), store: "users", exposed: true},
		{name: "own stm visible", ctx: sop.ContextWithAuth(context.Background(), sop.AuthContext{UserID: "alice"}), store: "stm_alice", exposed: true},
		{name: "other user stm hidden", ctx: sop.ContextWithAuth(context.Background(), sop.AuthContext{UserID: "alice"}), store: "stm_bob", exposed: false},
		{name: "time index hidden", ctx: sop.ContextWithAuth(context.Background(), sop.AuthContext{UserID: "alice"}), store: "stm_alice_by_time", exposed: false},
		{name: "sessions hidden for non-admin", ctx: sop.ContextWithAuth(context.Background(), sop.AuthContext{UserID: "alice"}), store: "sessions", exposed: false},
		{name: "sessions visible for admin", ctx: sop.ContextWithAuth(context.Background(), sop.AuthContext{Roles: []string{sop.RoleAdmin}, UserID: "root"}), store: "sessions", exposed: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldExposeStoreInRBAC(tc.ctx, tc.store); got != tc.exposed {
				t.Fatalf("shouldExposeStoreInRBAC(%q) = %v, want %v", tc.store, got, tc.exposed)
			}
		})
	}
}
