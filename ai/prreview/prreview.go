// Package prreview posts an AI-generated code review comment on a GitHub
// pull request by sending its diff to the Gemini API.
package prreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// DefaultModel is used when no model override is provided.
const DefaultModel = "gemini-2.5-flash"

// DefaultMaxDiffBytes bounds how much diff text is sent to Gemini so a large
// PR can't blow through the model's input token limit.
const DefaultMaxDiffBytes = 60000

const commentMarker = "<!-- gemini-pr-review -->"

// Event is the subset of the GitHub Actions pull_request event payload
// (GITHUB_EVENT_PATH) needed to locate the PR.
type Event struct {
	Number      int `json:"number"`
	PullRequest struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

// ParseEvent reads the pull_request event payload GitHub Actions writes to
// GITHUB_EVENT_PATH and returns the repo owner, repo name, and PR number.
func ParseEvent(r io.Reader) (owner, repo string, prNumber int, err error) {
	var ev Event
	if err := json.NewDecoder(r).Decode(&ev); err != nil {
		return "", "", 0, fmt.Errorf("failed to decode event payload: %w", err)
	}

	prNumber = ev.PullRequest.Number
	if prNumber == 0 {
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

// ReviewDiff sends prompt to the Gemini generateContent API and returns the
// model's response text.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read gemini response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini api error (status %d): %s", resp.StatusCode, string(body))
	}

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
