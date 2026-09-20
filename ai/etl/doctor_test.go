package etl

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const fixtureDoctorCSV = `Disease,Symptom_1,Symptom_2,Symptom_3
Fungal infection,itching,skin_rash,
Common Cold,cough,fever,fatigue
,phantom,should be skipped,
`

func TestPrepareDoctorDataset_ParsesCSVIntoConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(fixtureDoctorCSV))
	}))
	defer srv.Close()

	cfg, err := PrepareDoctorDataset(srv.URL)
	if err != nil {
		t.Fatalf("PrepareDoctorDataset failed: %v", err)
	}

	if cfg.ID != "doctor" {
		t.Errorf("expected config ID %q, got %q", "doctor", cfg.ID)
	}
	if cfg.Embedder.Type != "agent" || cfg.Embedder.AgentID != "nurse_local" {
		t.Errorf("expected embedder agent/nurse_local, got %+v", cfg.Embedder)
	}
	if len(cfg.Policies) != 1 || cfg.Policies[0].Type != "profanity" {
		t.Errorf("expected one profanity policy, got %+v", cfg.Policies)
	}

	// The row with an empty Disease column must be skipped, so only the
	// two real cases should turn into DataItems.
	if len(cfg.Data) != 2 {
		t.Fatalf("expected 2 data items (blank-disease row skipped), got %d: %+v", len(cfg.Data), cfg.Data)
	}

	first := cfg.Data[0]
	if first.ID != "fungal_infection_1" {
		t.Errorf("expected ID %q, got %q", "fungal_infection_1", first.ID)
	}
	if first.Text != "Fungal infection" {
		t.Errorf("expected Text %q, got %q", "Fungal infection", first.Text)
	}
	// Underscores in symptom text get replaced with spaces.
	wantDesc := "Case of Fungal infection. Symptoms include itching, skin rash."
	if first.Description != wantDesc {
		t.Errorf("expected Description %q, got %q", wantDesc, first.Description)
	}

	second := cfg.Data[1]
	if second.ID != "common_cold_2" {
		t.Errorf("expected ID %q, got %q", "common_cold_2", second.ID)
	}
	wantDesc2 := "Case of Common Cold. Symptoms include cough, fever, fatigue."
	if second.Description != wantDesc2 {
		t.Errorf("expected Description %q, got %q", wantDesc2, second.Description)
	}
}

func TestPrepareDoctorDataset_PropagatesDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// A 500 response is still a valid HTTP response (http.Get won't
	// return an error for it), but the body won't be valid CSV with a
	// usable header, so this exercises the CSV-header-read error path
	// instead of the network-error path.
	_, err := PrepareDoctorDataset(srv.URL + "/does-not-matter")
	if err == nil {
		t.Fatal("expected an error parsing an empty error-page body as CSV")
	}
}

func TestPrepareDoctorDataset_UnreachableURLReturnsError(t *testing.T) {
	_, err := PrepareDoctorDataset("http://127.0.0.1:1/unreachable")
	if err == nil {
		t.Fatal("expected an error downloading from an unreachable address")
	}
}
