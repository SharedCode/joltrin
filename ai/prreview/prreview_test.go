package prreview

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

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
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Header:     make(http.Header),
		}, nil
	})

	if _, err := ReviewDiff(context.Background(), "test-key", DefaultModel, "prompt"); err == nil {
		t.Fatal("expected an error when Gemini returns a non-200 status")
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
