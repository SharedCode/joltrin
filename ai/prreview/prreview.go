// Package prreview posts an AI-generated code review comment on a GitHub
// pull request by sending its diff to the Gemini API.
package prreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
)

// DefaultModel is used when no model override is provided.
const DefaultModel = "gemini-2.5-flash"

// DefaultMaxDiffBytes bounds how much diff text is sent to Gemini so a large
// PR can't blow through the model's input token limit.
const DefaultMaxDiffBytes = 60000

const commentMarker = "<!-- gemini-pr-review -->"
const remediationCommentMarker = "<!-- gemini-pr-remediation -->"

// Event is the subset of the GitHub Actions event payload (GITHUB_EVENT_PATH)
// needed to locate the PR. It covers three trigger shapes: pull_request,
// issue_comment (a PR comment such as "/gemini review"), and workflow_dispatch
// (manual run from the Actions tab, which carries no PR context of its own so
// the PR number is passed as an input).
type Event struct {
	Number      int `json:"number"`
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Issue struct {
		Number int `json:"number"`
		// PullRequest is present (non-null) only when the commented-on
		// issue is actually a pull request.
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	Inputs struct {
		PRNumber string `json:"pr_number"`
	} `json:"inputs"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

// isPullRequestComment reports whether an issue_comment event fired on a
// pull request rather than a plain issue.
func (e Event) isPullRequestComment() bool {
	return len(e.Issue.PullRequest) > 0 && string(e.Issue.PullRequest) != "null"
}

// ParseEvent reads the GitHub Actions event payload written to
// GITHUB_EVENT_PATH and returns the repo owner, repo name, and PR number. It
// handles pull_request, issue_comment, and workflow_dispatch payloads.
func ParseEvent(r io.Reader) (owner, repo string, prNumber int, err error) {
	var ev Event
	if err := json.NewDecoder(r).Decode(&ev); err != nil {
		return "", "", 0, fmt.Errorf("failed to decode event payload: %w", err)
	}

	switch {
	case ev.PullRequest.Number != 0:
		prNumber = ev.PullRequest.Number
	case ev.Issue.Number != 0:
		if !ev.isPullRequestComment() {
			return "", "", 0, fmt.Errorf("issue comment is not on a pull request")
		}
		prNumber = ev.Issue.Number
	case ev.Inputs.PRNumber != "":
		prNumber, err = strconv.Atoi(ev.Inputs.PRNumber)
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid pr_number input %q: %w", ev.Inputs.PRNumber, err)
		}
	default:
		prNumber = ev.Number
	}

	if prNumber == 0 {
		return "", "", 0, fmt.Errorf("event payload has no pull request number")
	}

	owner = ev.Repository.Owner.Login
	repo = ev.Repository.Name
	if owner == "" || repo == "" {
		return "", "", 0, fmt.Errorf("event payload is missing repository owner/name")
	}

	return owner, repo, prNumber, nil
}

// FetchDiff downloads the unified diff for a pull request via the GitHub API.
func FetchDiff(ctx context.Context, token, owner, repo string, prNumber int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, prNumber)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create diff request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github diff request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read diff response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api error fetching diff (status %d): %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

// FetchHeadSHA looks up the current head commit SHA for a pull request via
// the GitHub API.
func FetchHeadSHA(ctx context.Context, token, owner, repo string, prNumber int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, prNumber)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create pull request request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github pull request request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read pull request response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api error fetching pull request (status %d): %s", resp.StatusCode, string(body))
	}

	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", fmt.Errorf("failed to decode pull request response: %w", err)
	}
	if pr.Head.SHA == "" {
		return "", fmt.Errorf("pull request response has no head sha")
	}

	return pr.Head.SHA, nil
}

// FailingCheck is a single non-passing check run against a commit.
type FailingCheck struct {
	Name    string
	Summary string
}

// FetchFailingChecks returns the check runs on sha that did not pass, for use
// as remediation context. Checks that are still queued/in-progress, or that
// passed/were skipped/neutral, are excluded.
func FetchFailingChecks(ctx context.Context, token, owner, repo, sha string) ([]FailingCheck, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s/check-runs", owner, repo, sha)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create check-runs request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github check-runs request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read check-runs response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api error fetching check runs (status %d): %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			Output     struct {
				Summary string `json:"summary"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode check-runs response: %w", err)
	}

	var failing []FailingCheck
	for _, run := range parsed.CheckRuns {
		switch run.Conclusion {
		case "failure", "timed_out", "cancelled", "action_required":
			failing = append(failing, FailingCheck{Name: run.Name, Summary: run.Output.Summary})
		}
	}

	return failing, nil
}

// TruncateDiff caps diff at maxBytes so the Gemini request stays within the
// model's input token budget. It reports whether truncation happened.
func TruncateDiff(diff string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(diff) <= maxBytes {
		return diff, false
	}
	return diff[:maxBytes], true
}

const reviewPromptTemplate = `You are reviewing a GitHub pull request diff for a Go codebase. Evaluate the diff below for:
- Code quality and readability
- Potential bugs and correctness issues
- Missed edge cases
- Performance considerations

Respond in Markdown. Use a short bullet list per category, and omit any category with nothing to report. If the diff looks fine, say so briefly instead of inventing issues.

Diff:
%s
`

// BuildPrompt wraps a diff in the instructions sent to Gemini.
func BuildPrompt(diff string) string {
	return fmt.Sprintf(reviewPromptTemplate, diff)
}

const remediationPromptTemplate = `You are diagnosing a GitHub pull request for a Go codebase that has one or
more failing CI checks. You cannot run commands or edit files - you can only
read the diff and check output below.

Do:
- Identify the root cause of each failing check.
- Propose the minimal fix as a unified diff (` + "```diff" + ` code block).
- Briefly explain the root cause for each fix.

Do not:
- Invent a fix for a check that isn't listed as failing.
- Claim you applied the fix - you are only proposing a patch for a maintainer to review and apply by hand.

Failing checks:
%s

Diff:
%s
`

// BuildRemediationPrompt wraps a diff and its failing checks in the
// instructions sent to Gemini for suggest-only remediation.
func BuildRemediationPrompt(diff string, failingChecks []FailingCheck) string {
	checksText := "(none reported; diagnose from the diff alone)"
	if len(failingChecks) > 0 {
		checksText = ""
		for _, check := range failingChecks {
			checksText += fmt.Sprintf("- %s: %s\n", check.Name, check.Summary)
		}
	}
	return fmt.Sprintf(remediationPromptTemplate, checksText, diff)
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiGenerateRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiGenerateResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

// geminiMaxAttempts and geminiRetryBaseDelay are vars, not consts, so tests
// can shrink them and keep retry tests fast.
var (
	geminiMaxAttempts    = 5
	geminiRetryBaseDelay = 2 * time.Second
)

const geminiRequestTimeout = 30 * time.Second

// isRetryableGeminiStatus reports whether a Gemini HTTP status code is worth
// retrying: rate limiting and the transient 5xx statuses Gemini returns when
// a model is overloaded (e.g. "503 UNAVAILABLE ... high demand").
func isRetryableGeminiStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// ReviewDiff sends prompt to the Gemini generateContent API and returns the
// model's response text. Transient errors (429, 500, 502, 503, 504) are
// retried with exponential backoff and jitter before giving up.
func ReviewDiff(ctx context.Context, apiKey, model, prompt string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("gemini api key is missing")
	}
	if model == "" {
		model = DefaultModel
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	reqBody := geminiGenerateRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < geminiMaxAttempts; attempt++ {
		status, body, err := doGeminiRequest(ctx, url, jsonBody)
		if err != nil {
			return "", err
		}

		if status == http.StatusOK {
			return parseGeminiResponse(body)
		}

		lastErr = fmt.Errorf("gemini api error (status %d): %s", status, string(body))
		if !isRetryableGeminiStatus(status) || attempt == geminiMaxAttempts-1 {
			return "", lastErr
		}

		delay := geminiRetryBaseDelay << attempt
		jitter := time.Duration(rand.Int63n(int64(delay/2) + 1))
		timer := time.NewTimer(delay + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}

	return "", lastErr
}

// doGeminiRequest performs a single Gemini generateContent call, bounded by
// geminiRequestTimeout, and returns its status code and raw response body.
func doGeminiRequest(ctx context.Context, url string, jsonBody []byte) (int, []byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, geminiRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gemini api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read gemini response: %w", err)
	}

	return resp.StatusCode, body, nil
}

// parseGeminiResponse extracts the model's response text from a successful
// Gemini generateContent response body.
func parseGeminiResponse(body []byte) (string, error) {
	var genResp geminiGenerateResponse
	if err := json.Unmarshal(body, &genResp); err != nil {
		return "", fmt.Errorf("failed to decode gemini response: %w", err)
	}

	if len(genResp.Candidates) == 0 || len(genResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini response had no candidates")
	}

	var out string
	for _, part := range genResp.Candidates[0].Content.Parts {
		out += part.Text
	}
	if out == "" {
		return "", fmt.Errorf("gemini response text was empty")
	}

	return out, nil
}

// FormatComment wraps the model's review text in a header identifying it as
// an automated Gemini review, plus a truncation notice when the diff sent to
// Gemini was cut down. It's marked with a hidden comment marker so future
// runs on the same PR can be told apart from human comments if needed.
func FormatComment(review string, diffTruncated bool) string {
	comment := commentMarker + "\n## Gemini PR Review\n\n"
	if diffTruncated {
		comment += "_Diff was truncated before review; only the first part of the change was evaluated._\n\n"
	}
	comment += review
	return comment
}

// FormatRemediationComment wraps the model's suggested fix in a header
// identifying it as an automated, suggest-only Gemini remediation, plus a
// truncation notice when the diff sent to Gemini was cut down. It's marked
// with a hidden comment marker distinct from FormatComment's so review and
// remediation comments on the same PR can be told apart.
func FormatRemediationComment(review string, diffTruncated bool) string {
	comment := remediationCommentMarker + "\n## Gemini PR Remediation (suggest-only)\n\n"
	comment += "_This is a suggested fix, not applied automatically. Review and apply it by hand._\n\n"
	if diffTruncated {
		comment += "_Diff was truncated before review; only the first part of the change was evaluated._\n\n"
	}
	comment += review
	return comment
}

type githubCommentRequest struct {
	Body string `json:"body"`
}

// PostComment posts body as a new comment on the pull request via the
// GitHub issues API (pull requests are issues for commenting purposes).
func PostComment(ctx context.Context, token, owner, repo string, prNumber int, body string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, prNumber)

	jsonBody, err := json.Marshal(githubCommentRequest{Body: body})
	if err != nil {
		return fmt.Errorf("failed to marshal comment request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create comment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("github comment request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github api error posting comment (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// OpenEventFile opens the file at GITHUB_EVENT_PATH (path), returning it for
// ParseEvent to read. Callers are responsible for closing it.
func OpenEventFile(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open event payload %q: %w", path, err)
	}
	return f, nil
}
