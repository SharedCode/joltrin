package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	if err := probe(client, srv.URL); err != nil {
		t.Fatalf("expected healthy, got error: %v", err)
	}
}

func TestProbeUnhealthyStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	if err := probe(client, srv.URL); err == nil {
		t.Fatal("expected error for 503 response, got nil")
	}
}

func TestProbeConnectionRefused(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	if err := probe(client, "http://127.0.0.1:1"); err == nil {
		t.Fatal("expected error for unreachable endpoint, got nil")
	}
}
