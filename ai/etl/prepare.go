package etl

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/internal/netguard"
)

// PrepareData downloads a CSV dataset and converts it to the agent DataItem JSON format.
func PrepareData(ctx context.Context, url, out string, limit int) error {
	if err := netguard.ValidateFetchURL(url); err != nil {
		return fmt.Errorf("dataset URL rejected: %w", err)
	}

	fmt.Printf("Downloading dataset from %s...\n", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := netguard.SafeClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to download dataset: %w", err)
	}
	defer resp.Body.Close()

	reader := csv.NewReader(resp.Body)

	// Read header
	_, err = reader.Read()
	if err != nil {
		return fmt.Errorf("failed to read CSV header: %w", err)
	}

	var items []agent.DataItem
	count := 0

	for {
		if limit > 0 && count >= limit {
			break
		}
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read CSV record: %w", err)
		}

		// First column is Disease
		disease := strings.TrimSpace(record[0])
		if disease == "" {
			continue
		}

		// Remaining columns are symptoms
		var symptoms []string
		for i := 1; i < len(record); i++ {
			s := strings.TrimSpace(record[i])
			if s != "" {
				s = strings.ReplaceAll(s, "_", " ")
				s = strings.ReplaceAll(s, "cold hands", "freezing hands")
				symptoms = append(symptoms, s)
			}
		}

		// Create item
		baseID := strings.ToLower(strings.ReplaceAll(disease, " ", "_"))

		// Generate deterministic ID based on content
		h := fnv.New32a()
		h.Write([]byte(disease))
		for _, s := range symptoms {
			h.Write([]byte(s))
		}
		id := fmt.Sprintf("%s_%x", baseID, h.Sum32())

		desc := fmt.Sprintf("Symptoms: %s", strings.Join(symptoms, ", "))

		items = append(items, agent.DataItem{
			ID:          id,
			Text:        disease,
			Description: desc,
		})
		count++
	}

	fmt.Printf("Processed %d records.\n", count)

	// Add manual entries for missing diseases
	items = append(items, agent.DataItem{
		ID:          "common_cold_001",
		Text:        "Common Cold",
		Description: "Symptoms: cough, runny nose, sneezing, sore throat, congestion, mild fever, body aches",
	})
	items = append(items, agent.DataItem{
		ID:          "influenza_001",
		Text:        "Influenza",
		Description: "Symptoms: high fever, chills, muscle aches, cough, congestion, runny nose, headaches, fatigue",
	})

	// Write to JSON
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(items); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	fmt.Printf("Saved to %s\n", out)
	return nil
}
