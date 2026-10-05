package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// fakeGitHub は githubAPI を httptest のサーバーへ差し替え、テストの終わりに戻す。
func fakeGitHub(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := githubAPI
	githubAPI = srv.URL
	t.Cleanup(func() { githubAPI = old })
}

func TestIssueBodyJoinsTitleAndBody(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues/42" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		w.Write([]byte(`{"title":"  落ちる  ","body":"手順はこう\n"}`))
	})

	got, err := issueBody(context.Background(), "o/r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if want := "落ちる  \n\n手順はこう"; got != want {
		t.Errorf("issueBody = %q, want %q", got, want)
	}
}

func TestIssueBodyWithEmptyBodyIsTitleOnly(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"title":"題だけ","body":null}`))
	})

	got, err := issueBody(context.Background(), "o/r", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "題だけ" {
		t.Errorf("issueBody = %q", got)
	}
}

func TestHeadSHAFollowsDefaultBranch(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			w.Write([]byte(`{"default_branch":"trunk"}`))
		case "/repos/o/r/commits/trunk":
			w.Write([]byte(`{"sha":"` + sha + `"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	got, err := headSHA(context.Background(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if got != sha {
		t.Errorf("headSHA = %q, want %q", got, sha)
	}
}

func TestGithubJSONNon200IsError(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})

	var out struct{}
	err := githubJSON(context.Background(), "/repos/o/missing", &out)
	if err == nil {
		t.Fatal("非 200 なのに err が nil")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "/repos/o/missing") {
		t.Errorf("err = %v（状態とパスを含んでほしい）", err)
	}
}

func TestHeadSHAStopsOnFirstError(t *testing.T) {
	calls := 0
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "rate limited", http.StatusForbidden)
	})

	if _, err := headSHA(context.Background(), "o/r"); err == nil {
		t.Fatal("403 なのに err が nil")
	}
	if calls != 1 {
		t.Errorf("要求数 = %d, want 1", calls)
	}
}

func TestAuthorizationOnlyWithToken(t *testing.T) {
	var got string
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	})
	var out struct{}

	t.Setenv("GITHUB_TOKEN", "")
	if err := githubJSON(context.Background(), "/x", &out); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("トークンが無いのに Authorization = %q", got)
	}

	t.Setenv("GITHUB_TOKEN", "abc")
	if err := githubJSON(context.Background(), "/x", &out); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer abc" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer abc")
	}
}

// 以下は実際の GitHub API へ出る。CLEW_LIVE=1 のときだけ走る（docker compose run --rm test-live）。

func skipUnlessLive(t *testing.T) {
	t.Helper()
	if os.Getenv("CLEW_LIVE") != "1" {
		t.Skip("CLEW_LIVE=1 のときだけ実接続する")
	}
}

func TestLiveIssueBody(t *testing.T) {
	skipUnlessLive(t)
	// golang/go#1 は公開リポジトリの最初の Issue で、消えることはない。
	// 題の文言は改題されうるので固定せず、空でないことだけを確かめる。
	got, err := issueBody(context.Background(), "golang/go", 1)
	if err != nil {
		t.Fatal(err)
	}
	title := strings.TrimSpace(firstLine(got))
	if title == "" {
		t.Fatalf("題が空: %q", got)
	}
	t.Logf("golang/go#1 の題: %q", title)
}

func TestLiveHeadSHA(t *testing.T) {
	skipUnlessLive(t)
	got, err := headSHA(context.Background(), "golang/go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(got) {
		t.Errorf("headSHA = %q（40 桁の hex ではない）", got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
