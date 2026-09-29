// Command pr-reviewer reviews the pull request described by GITHUB_EVENT_PATH
// with Gemini, posts the findings as a PR comment, and records the result as
// the "Gemini Review" commit status that branch protection requires. It is
// meant to run as a GitHub Actions step; see
// .github/workflows/gemini-pr-review.yml.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sharedcode/joltrin/ai/prreview"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "::error::%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	eventPath := os.Getenv("GITHUB_EVENT_PATH")
	if eventPath == "" {
		return fmt.Errorf("GITHUB_EVENT_PATH is not set")
	}

	eventFile, err := prreview.OpenEventFile(eventPath)
	if err != nil {
		return err
	}
	defer eventFile.Close()

	owner, repo, prNumber, err := prreview.ParseEvent(eventFile)
	if err != nil {
		return err
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = prreview.DefaultModel
	}

	if raw := os.Getenv("GEMINI_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			prreview.GeminiRequestTimeout = parsed
		}
	}

	maxDiffBytes := prreview.DefaultMaxDiffBytes
	if raw := os.Getenv("GEMINI_MAX_DIFF_BYTES"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxDiffBytes = parsed
		}
	}

	// RunGate fails closed: any problem sets a failing Gemini Review status
	// and returns an error, which fails this job.
	return prreview.RunGate(context.Background(), prreview.GateConfig{
		Token:        token,
		APIKey:       os.Getenv("GEMINI_API_KEY"),
		Model:        model,
		Owner:        owner,
		Repo:         repo,
		PRNumber:     prNumber,
		MaxDiffBytes: maxDiffBytes,
	})
}
