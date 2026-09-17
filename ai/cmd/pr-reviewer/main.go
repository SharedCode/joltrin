// Command pr-reviewer fetches the diff for the pull request described by
// GITHUB_EVENT_PATH, asks Gemini to review it, and posts the result as a PR
// comment. It's meant to run as a GitHub Actions step; see
// .github/workflows/gemini-pr-review.yml.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/sharedcode/joltrin/ai/prreview"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "::error::%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "::warning::GEMINI_API_KEY is not set, skipping Gemini PR review")
		return nil
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "::warning::GITHUB_TOKEN is not set, skipping Gemini PR review")
		return nil
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

	maxDiffBytes := prreview.DefaultMaxDiffBytes
	if raw := os.Getenv("GEMINI_MAX_DIFF_BYTES"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxDiffBytes = parsed
		}
	}

	ctx := context.Background()

	diff, err := prreview.FetchDiff(ctx, token, owner, repo, prNumber)
	if err != nil {
		return err
	}

	if diff == "" {
		fmt.Fprintln(os.Stderr, "::warning::pull request diff is empty, skipping Gemini PR review")
		return nil
	}

	truncatedDiff, wasTruncated := prreview.TruncateDiff(diff, maxDiffBytes)
	prompt := prreview.BuildPrompt(truncatedDiff)

	review, err := prreview.ReviewDiff(ctx, apiKey, model, prompt)
	if err != nil {
		return err
	}

	comment := prreview.FormatComment(review, wasTruncated)

	if err := prreview.PostComment(ctx, token, owner, repo, prNumber, comment); err != nil {
		return err
	}

	fmt.Printf("Posted Gemini review comment on %s/%s#%d\n", owner, repo, prNumber)
	return nil
}
