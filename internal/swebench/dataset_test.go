package swebench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fixtureServer は offset に応じて testdata の応答を返す。実ネットワークへは出ない。
func fixtureServer(t *testing.T, calls *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rows" {
			t.Errorf("想定外のパス: %s", r.URL.Path)
		}
		q := r.URL.Query()
		*calls = append(*calls, q.Get("offset")+":"+q.Get("length"))

		offset, _ := strconv.Atoi(q.Get("offset"))
		name := "rows_offset0.json"
		if offset >= 2 {
			name = "rows_offset2.json"
		}
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
		if err != nil {
			t.Fatalf("fixture を読めない: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoadAllPages(t *testing.T) {
	var calls []string
	srv := fixtureServer(t, &calls)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })

	got, err := Load(context.Background(), "princeton-nlp/SWE-bench_Verified", 0)
	if err != nil {
		t.Fatalf("Load が失敗: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("件数 = %d, want 3", len(got))
	}
	if got[0].InstanceID != "astropy__astropy-12907" {
		t.Errorf("InstanceID = %q", got[0].InstanceID)
	}
	if got[0].BaseCommit != "d16bfe05a744909de4b27f5875fe0d4ed41ce607" {
		t.Errorf("BaseCommit = %q", got[0].BaseCommit)
	}
	if got[2].Repo != "sympy/sympy" {
		t.Errorf("Repo = %q", got[2].Repo)
	}
	if len(calls) != 2 {
		t.Fatalf("呼び出し回数 = %d (%v), want 2", len(calls), calls)
	}
	if calls[0] != "0:100" {
		t.Errorf("1 回目の問合せ = %q, want 0:100", calls[0])
	}
	if calls[1] != "2:100" {
		t.Errorf("2 回目の問合せ = %q, want 2:100", calls[1])
	}
}

func TestLoadRespectsLimit(t *testing.T) {
	var calls []string
	srv := fixtureServer(t, &calls)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })

	got, err := Load(context.Background(), "princeton-nlp/SWE-bench_Verified", 1)
	if err != nil {
		t.Fatalf("Load が失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("件数 = %d, want 1", len(got))
	}
	if len(calls) != 1 || calls[0] != "0:1" {
		t.Errorf("問合せ = %v, want [0:1]", calls)
	}
}

func TestLoadReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })

	if _, err := Load(context.Background(), "x/y", 1); err == nil {
		t.Fatal("エラーを返すはず")
	}
}
