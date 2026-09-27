package main

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestShouldExposeStoreInUI_HidesInternalStores(t *testing.T) {
	cases := []struct {
		name    string
		store   string
		userID  string
		isAdmin bool
		exposed bool
	}{
		{name: "plain store", store: "users", userID: "alice", exposed: true},
		{name: "component store hidden", store: "ltm_omni/items", userID: "alice", exposed: false},
		{name: "vector store hidden", store: "documents_vecs", userID: "alice", exposed: false},
		{name: "stm time index hidden", store: "stm_alice_by_time", userID: "alice", exposed: false},
		{name: "own stm visible", store: "stm_alice", userID: "alice", exposed: true},
		{name: "own ltm visible", store: "ltm_alice", userID: "alice", exposed: true},
		{name: "other user stm hidden", store: "stm_bob", userID: "alice", exposed: false},
		{name: "other user ltm hidden", store: "ltm_bob", userID: "alice", exposed: false},
		{name: "sessions hidden for non-admin", store: "sessions", userID: "alice", exposed: false},
		{name: "sessions visible for admin", store: "sessions", userID: "root", isAdmin: true, exposed: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.userID != "" {
				roles := []string{}
				if tc.isAdmin {
					roles = append(roles, sop.RoleAdmin)
				}
				ctx = sop.ContextWithAuth(ctx, sop.AuthContext{UserID: tc.userID, Roles: roles})
			}
			if got := shouldExposeStoreInRBAC(ctx, tc.store); got != tc.exposed {
				t.Fatalf("shouldExposeStoreInRBAC(%q) = %v, want %v", tc.store, got, tc.exposed)
			}
		})
	}
}
