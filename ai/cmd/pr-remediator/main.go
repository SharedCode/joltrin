// Command pr-remediator fetches the diff and failing checks for the pull
// request described by GITHUB_EVENT_PATH, asks Gemini to diagnose and
// propose a fix, and posts the suggestion as a PR comment. It never applies
// the fix itself; a maintainer reviews and applies it by hand. It's meant to
// run as a GitHub Actions step triggered by a "/gemini fix" PR comment; see
// .github/workflows/gemini-pr-remediation.yml.
package main

import (
	"context"
	"fmt"
	"os"
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
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "::warning::GEMINI_API_KEY is not set, skipping Gemini PR remediation")
		return nil
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "::warning::GITHUB_TOKEN is not set, skipping Gemini PR remediation")
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

	if raw := os.Getenv("GEMINI_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			prreview.GeminiRequestTimeout = parsed
		}
	}

	maxDiffBytes := prreview.DefaultMaxDiffBytes

	ctx := context.Background()

	headSHA, err := prreview.FetchHeadSHA(ctx, token, owner, repo, prNumber)
	if err != nil {
		return err
	}

	diff, err := prreview.FetchDiff(ctx, token, owner, repo, prNumber)
	if err != nil {
		return err
	}

	if diff == "" {
		fmt.Fprintln(os.Stderr, "::warning::pull request diff is empty, skipping Gemini PR remediation")
		return nil
	}

	failingChecks, err := prreview.FetchFailingChecks(ctx, token, owner, repo, headSHA)
	if err != nil {
		return err
	}

	truncatedDiff, wasTruncated := prreview.TruncateDiff(diff, maxDiffBytes)
	prompt := prreview.BuildRemediationPrompt(truncatedDiff, failingChecks)

	suggestion, err := prreview.ReviewDiff(ctx, apiKey, model, prompt)
	if err != nil {
		return err
	}

	comment := prreview.FormatRemediationComment(suggestion, wasTruncated)

	if err := prreview.PostComment(ctx, token, owner, repo, prNumber, comment); err != nil {
		return err
	}

	fmt.Printf("Posted Gemini remediation comment on %s/%s#%d\n", owner, repo, prNumber)
	return nil
}
