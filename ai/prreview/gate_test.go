package prreview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    Verdict
		body    string
		wantErr bool
	}{
		{"pass", "Looks fine.\n\nVERDICT: PASS\n", VerdictPass, "Looks fine.", false},
		{"fail", "- bug in x\nVERDICT: FAIL", VerdictFail, "- bug in x", false},
		{"verdict only", "VERDICT: PASS", VerdictPass, "", false},
		{"missing", "Looks fine.", "", "", true},
		{"empty", "", "", "", true},
		{"reworded", "ok\nVerdict: pass", "", "", true},
		{"decorated", "ok\n**VERDICT: PASS**", "", "", true},
		{"verdict not last", "VERDICT: PASS\nbut wait, there is a bug", "", "", true},
		{"both", "VERDICT: PASS\nVERDICT: FAIL", VerdictFail, "VERDICT: PASS", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, v, err := ParseVerdict(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got verdict %q", v)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != tc.want || body != tc.body {
				t.Fatalf("got (%q, %q), want (%q, %q)", body, v, tc.body, tc.want)
			}
		})
	}
}

func TestBuildGatePromptIncludesDisputesAndFormat(t *testing.T) {
	p := BuildGatePrompt("diff --git a/x b/x", []string{"/gemini dispute finding 1"})
	for _, want := range []string{"diff --git a/x b/x", "/gemini dispute finding 1", verdictPassLine, verdictFailLine, "untrusted"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	if !strings.Contains(BuildGatePrompt("d", nil), "(none)") {
		t.Fatal("prompt should say (none) with no disputes")
	}
}

func TestFetchDisputesOnlyTrustedAssociations(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		body := `[
			{"body":"/gemini dispute owner claim","author_association":"OWNER"},
			{"body":"/gemini dispute stranger claim","author_association":"NONE"},
			{"body":"just a comment","author_association":"MEMBER"},
			{"body":"/gemini dispute collab claim","author_association":"COLLABORATOR"},
			{"body":"/gemini dispute contributor claim","author_association":"CONTRIBUTOR"}
		]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})

	got, err := FetchDisputes(context.Background(), "t", "sharedcode", "joltrin", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || !strings.Contains(got[0], "owner") || !strings.Contains(got[1], "collab") {
		t.Fatalf("got %v", got)
	}
}

func TestFetchDisputesErrorStatus(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})
	if _, err := FetchDisputes(context.Background(), "t", "o", "r", 1); err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestSetCommitStatusSendsContextAndSHA(t *testing.T) {
	var path string
	var sent commitStatusRequest
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		path = req.URL.Path
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &sent)
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})

	long := strings.Repeat("x", 300)
	if err := SetCommitStatus(context.Background(), "t", "sharedcode", "joltrin", "abc123", "failure", long); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/repos/sharedcode/joltrin/statuses/abc123" {
		t.Fatalf("got path %q", path)
	}
	if sent.Context != "Gemini Review" || sent.State != "failure" || len(sent.Description) != 140 {
		t.Fatalf("got %+v", sent)
	}
}

func TestSetCommitStatusErrorStatus(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})
	if err := SetCommitStatus(context.Background(), "t", "o", "r", "sha", "success", "d"); err == nil {
		t.Fatal("expected error on non-201")
	}
}

func TestFormatGateCommentRecordsShaAndVerdict(t *testing.T) {
	c := FormatGateComment("- bug", VerdictFail, "abc123")
	for _, want := range []string{commentMarker, "sha=abc123 verdict=FAIL", "blocks merge", "/gemini review"} {
		if !strings.Contains(c, want) {
			t.Fatalf("comment missing %q: %s", want, c)
		}
	}
	if strings.Contains(FormatGateComment("ok", VerdictPass, "abc"), "blocks merge") {
		t.Fatal("a passing comment must not say it blocks merge")
	}
}

type gateFake struct {
	diff       string
	geminiText string
	geminiCode int
	states     []string
	comments   []string
}

func (g *gateFake) install(t *testing.T) {
	withTransport(t, func(req *http.Request) (*http.Response, error) {
		resp := func(code int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
		switch {
		case strings.Contains(req.URL.Host, "generativelanguage"):
			if g.geminiCode != 0 {
				return resp(g.geminiCode, "{}")
			}
			b, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": g.geminiText}}}}}})
			return resp(200, string(b))
		case strings.HasSuffix(req.URL.Path, "/statuses/headsha"):
			raw, _ := io.ReadAll(req.Body)
			var s commitStatusRequest
			_ = json.Unmarshal(raw, &s)
			g.states = append(g.states, s.State)
			return resp(201, "{}")
		case strings.HasSuffix(req.URL.Path, "/comments") && req.Method == http.MethodPost:
			raw, _ := io.ReadAll(req.Body)
			g.comments = append(g.comments, string(raw))
			return resp(201, "{}")
		case strings.HasSuffix(req.URL.Path, "/comments"):
			return resp(200, "[]")
		case strings.HasSuffix(req.URL.Path, "/pulls/7") && strings.Contains(req.Header.Get("Accept"), "diff"):
			return resp(200, g.diff)
		case strings.HasSuffix(req.URL.Path, "/pulls/7"):
			return resp(200, `{"head":{"sha":"headsha"}}`)
		}
		t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		return nil, nil
	})
}

func gateCfg(key string) GateConfig {
	return GateConfig{Token: "t", APIKey: key, Owner: "o", Repo: "r", PRNumber: 7, MaxDiffBytes: 1000}
}

func lastState(g *gateFake) string {
	if len(g.states) == 0 {
		return ""
	}
	return g.states[len(g.states)-1]
}

func TestRunGatePassSetsSuccess(t *testing.T) {
	g := &gateFake{diff: "diff --git a/x b/x", geminiText: "Fine.\nVERDICT: PASS"}
	g.install(t)
	if err := RunGate(context.Background(), gateCfg("k")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lastState(g) != "success" || len(g.comments) != 1 {
		t.Fatalf("states=%v comments=%d", g.states, len(g.comments))
	}
}

func TestRunGateFailVerdictBlocks(t *testing.T) {
	g := &gateFake{diff: "diff --git a/x b/x", geminiText: "- bug\nVERDICT: FAIL"}
	g.install(t)
	if err := RunGate(context.Background(), gateCfg("k")); err == nil {
		t.Fatal("a FAIL verdict must fail the job")
	}
	if lastState(g) != "failure" {
		t.Fatalf("states=%v", g.states)
	}
}

func TestRunGateFailsClosed(t *testing.T) {
	cases := map[string]struct {
		key  string
		fake gateFake
	}{
		"no api key":     {"", gateFake{diff: "d"}},
		"empty diff":     {"k", gateFake{diff: "  \n"}},
		"truncated diff": {"k", gateFake{diff: strings.Repeat("x", 2000), geminiText: "VERDICT: PASS"}},
		"malformed":      {"k", gateFake{diff: "d", geminiText: "looks good to me"}},
		"gemini 400":     {"k", gateFake{diff: "d", geminiCode: 400}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := tc.fake
			g.install(t)
			if err := RunGate(context.Background(), gateCfg(tc.key)); err == nil {
				t.Fatal("expected an error")
			}
			if lastState(&g) != "failure" {
				t.Fatalf("final status must be failure, got %v", g.states)
			}
		})
	}
}
