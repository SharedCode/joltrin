package prreview

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withFastGeminiRetry(t *testing.T, maxAttempts int) {
	t.Helper()
	originalAttempts, originalDelay := geminiMaxAttempts, geminiRetryBaseDelay
	geminiMaxAttempts = maxAttempts
	geminiRetryBaseDelay = time.Millisecond
	t.Cleanup(func() {
		geminiMaxAttempts = originalAttempts
		geminiRetryBaseDelay = originalDelay
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withTransport(t *testing.T, fn roundTripFunc) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = fn
	t.Cleanup(func() { http.DefaultTransport = original })
}

func TestParseEventPullRequestNumber(t *testing.T) {
	payload := `{
		"pull_request": {"number": 42},
		"repository": {"name": "joltrin", "owner": {"login": "sharedcode"}}
	}`

	owner, repo, number, err := ParseEvent(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ParseEvent returned an unexpected error: %v", err)
	}
	if owner != "sharedcode" || repo != "joltrin" || number != 42 {
		t.Fatalf("got owner=%q repo=%q number=%d, want owner=sharedcode repo=joltrin number=42", owner, repo, number)
	}
}

func TestParseEventMissingNumber(t *testing.T) {
	payload := `{"repository": {"name": "joltrin", "owner": {"login": "sharedcode"}}}`

	if _, _, _, err := ParseEvent(strings.NewReader(payload)); err == nil {
		t.Fatal("expected an error when the event payload has no PR number")
	}
}

func TestParseEventIssueCommentOnPullRequest(t *testing.T) {
	payload := `{
		"action": "created",
		"comment": {"body": "/gemini review"},
		"issue": {"number": 42, "pull_request": {"url": "https://api.github.com/..."}},
		"repository": {"name": "joltrin", "owner": {"login": "sharedcode"}}
	}`

	owner, repo, number, err := ParseEvent(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ParseEvent returned an unexpected error: %v", err)
	}
	if owner != "sharedcode" || repo != "joltrin" || number != 42 {
		t.Fatalf("got owner=%q repo=%q number=%d, want owner=sharedcode repo=joltrin number=42", owner, repo, number)
	}
}

func TestParseEventIssueCommentOnPlainIssue(t *testing.T) {
	payload := `{
		"action": "created",
		"comment": {"body": "/gemini review"},
		"issue": {"number": 42},
		"repository": {"name": "joltrin", "owner": {"login": "sharedcode"}}
	}`

	if _, _, _, err := ParseEvent(strings.NewReader(payload)); err == nil {
		t.Fatal("expected an error when the comment is on a plain issue, not a pull request")
	}
}

func TestParseEventWorkflowDispatch(t *testing.T) {
	payload := `{
		"inputs": {"pr_number": "42", "model": "gemini-2.5-pro"},
		"repository": {"name": "joltrin", "owner": {"login": "sharedcode"}}
	}`

	owner, repo, number, err := ParseEvent(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ParseEvent returned an unexpected error: %v", err)
	}
	if owner != "sharedcode" || repo != "joltrin" || number != 42 {
		t.Fatalf("got owner=%q repo=%q number=%d, want owner=sharedcode repo=joltrin number=42", owner, repo, number)
	}
}

func TestParseEventWorkflowDispatchInvalidPRNumber(t *testing.T) {
	payload := `{
		"inputs": {"pr_number": "not-a-number"},
		"repository": {"name": "joltrin", "owner": {"login": "sharedcode"}}
	}`

	if _, _, _, err := ParseEvent(strings.NewReader(payload)); err == nil {
		t.Fatal("expected an error when the pr_number input isn't a number")
	}
}

func TestTruncateDiffUnderLimit(t *testing.T) {
	diff := "short diff"
	got, truncated := TruncateDiff(diff, 100)
	if truncated {
		t.Fatal("expected no truncation when diff is under the limit")
	}
	if got != diff {
		t.Fatalf("got %q, want %q", got, diff)
	}
}

func TestTruncateDiffOverLimit(t *testing.T) {
	diff := strings.Repeat("a", 200)
	got, truncated := TruncateDiff(diff, 50)
	if !truncated {
		t.Fatal("expected truncation when diff exceeds the limit")
	}
	if len(got) != 50 {
		t.Fatalf("got length %d, want 50", len(got))
	}
}

func TestFormatCommentIncludesTruncationNotice(t *testing.T) {
	comment := FormatComment("looks fine", true)
	if !strings.Contains(comment, "truncated") {
		t.Fatalf("expected truncation notice in comment, got %q", comment)
	}
	if !strings.Contains(comment, commentMarker) {
		t.Fatalf("expected comment marker in comment, got %q", comment)
	}
}

func TestFormatCommentWithoutTruncation(t *testing.T) {
	comment := FormatComment("looks fine", false)
	if strings.Contains(comment, "truncated") {
		t.Fatalf("did not expect truncation notice in comment, got %q", comment)
	}
}

func TestReviewDiffMissingAPIKey(t *testing.T) {
	if _, err := ReviewDiff(context.Background(), "", DefaultModel, "prompt"); err == nil {
		t.Fatal("expected an error when the Gemini API key is missing")
	}
}

func TestReviewDiffParsesResponse(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.String(), "gemini-2.5-flash") {
			t.Fatalf("expected request URL to reference the model, got %s", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"no issues found"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	got, err := ReviewDiff(context.Background(), "test-key", DefaultModel, "prompt")
	if err != nil {
		t.Fatalf("ReviewDiff returned an unexpected error: %v", err)
	}
	if got != "no issues found" {
		t.Fatalf("got %q, want %q", got, "no issues found")
	}
}

func TestReviewDiffErrorStatus(t *testing.T) {
	withFastGeminiRetry(t, 3)

	nonRetryable := false
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		nonRetryable = true
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad api key"}`)),
			Header:     make(http.Header),
		}, nil
	})

	if _, err := ReviewDiff(context.Background(), "test-key", DefaultModel, "prompt"); err == nil {
		t.Fatal("expected an error when Gemini returns a non-200 status")
	}
	if !nonRetryable {
		t.Fatal("expected the transport to be called")
	}
}

func TestReviewDiffRetriesOnTransientStatus(t *testing.T) {
	withFastGeminiRetry(t, 5)

	var attempts atomic.Int32
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		if attempts.Add(1) < 3 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader(`{"error":"high demand"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"no issues found"}]}}]}`)),
			Header:     make(http.Header),
		}, nil
	})

	got, err := ReviewDiff(context.Background(), "test-key", DefaultModel, "prompt")
	if err != nil {
		t.Fatalf("ReviewDiff returned an unexpected error: %v", err)
	}
	if got != "no issues found" {
		t.Fatalf("got %q, want %q", got, "no issues found")
	}
	if attempts.Load() != 3 {
		t.Fatalf("got %d attempts, want 3", attempts.Load())
	}
}

func TestReviewDiffGivesUpAfterMaxAttempts(t *testing.T) {
	withFastGeminiRetry(t, 3)

	var attempts atomic.Int32
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader(`{"error":"high demand"}`)),
			Header:     make(http.Header),
		}, nil
	})

	if _, err := ReviewDiff(context.Background(), "test-key", DefaultModel, "prompt"); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if attempts.Load() != 3 {
		t.Fatalf("got %d attempts, want 3 (geminiMaxAttempts)", attempts.Load())
	}
}

func TestFetchDiffSendsAuthAndDiffHeaders(t *testing.T) {
	var seenAuth, seenAccept string
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		seenAuth = req.Header.Get("Authorization")
		seenAccept = req.Header.Get("Accept")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("diff --git a/x b/x")),
			Header:     make(http.Header),
		}, nil
	})

	diff, err := FetchDiff(context.Background(), "test-token", "sharedcode", "joltrin", 42)
	if err != nil {
		t.Fatalf("FetchDiff returned an unexpected error: %v", err)
	}
	if diff != "diff --git a/x b/x" {
		t.Fatalf("got diff %q", diff)
	}
	if seenAuth != "Bearer test-token" {
		t.Fatalf("got Authorization header %q, want Bearer test-token", seenAuth)
	}
	if seenAccept != "application/vnd.github.v3.diff" {
		t.Fatalf("got Accept header %q, want application/vnd.github.v3.diff", seenAccept)
	}
}

func TestFetchHeadSHA(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"head":{"sha":"abc123"}}`)),
			Header:     make(http.Header),
		}, nil
	})

	sha, err := FetchHeadSHA(context.Background(), "test-token", "sharedcode", "joltrin", 42)
	if err != nil {
		t.Fatalf("FetchHeadSHA returned an unexpected error: %v", err)
	}
	if sha != "abc123" {
		t.Fatalf("got sha %q, want abc123", sha)
	}
}

func TestFetchFailingChecksFiltersPassingRuns(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"check_runs":[
				{"name":"lint","conclusion":"success","output":{"summary":"ok"}},
				{"name":"gitleaks","conclusion":"failure","output":{"summary":"secret found"}},
				{"name":"tests","conclusion":"neutral","output":{"summary":"skipped"}}
			]}`)),
			Header: make(http.Header),
		}, nil
	})

	failing, err := FetchFailingChecks(context.Background(), "test-token", "sharedcode", "joltrin", "abc123")
	if err != nil {
		t.Fatalf("FetchFailingChecks returned an unexpected error: %v", err)
	}
	if len(failing) != 1 || failing[0].Name != "gitleaks" || failing[0].Summary != "secret found" {
		t.Fatalf("got %+v, want a single gitleaks failure", failing)
	}
}

func TestBuildRemediationPromptIncludesFailingChecks(t *testing.T) {
	prompt := BuildRemediationPrompt("diff --git a/x b/x", []FailingCheck{{Name: "gitleaks", Summary: "secret found"}})
	if !strings.Contains(prompt, "gitleaks: secret found") {
		t.Fatalf("expected prompt to include failing check details, got %q", prompt)
	}
}

func TestBuildRemediationPromptNoFailingChecks(t *testing.T) {
	prompt := BuildRemediationPrompt("diff --git a/x b/x", nil)
	if !strings.Contains(prompt, "none reported") {
		t.Fatalf("expected prompt to note no reported failing checks, got %q", prompt)
	}
}

func TestFormatRemediationCommentMarksSuggestOnly(t *testing.T) {
	comment := FormatRemediationComment("```diff\n+fix\n```", false)
	if !strings.Contains(comment, remediationCommentMarker) {
		t.Fatalf("expected remediation comment marker, got %q", comment)
	}
	if !strings.Contains(comment, "not applied automatically") {
		t.Fatalf("expected suggest-only notice, got %q", comment)
	}
}

func TestPostCommentErrorStatus(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"message":"forbidden"}`)),
			Header:     make(http.Header),
		}, nil
	})

	err := PostComment(context.Background(), "test-token", "sharedcode", "joltrin", 42, "body")
	if err == nil {
		t.Fatal("expected an error when GitHub returns a non-201 status")
	}
}

func TestPostCommentSuccess(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":1}`)),
			Header:     make(http.Header),
		}, nil
	})

	if err := PostComment(context.Background(), "test-token", "sharedcode", "joltrin", 42, "body"); err != nil {
		t.Fatalf("PostComment returned an unexpected error: %v", err)
	}
}
