package repo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarGz は試験用の tar.gz を作る。実ネットワークへは出ない。
func makeTarGz(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		h := &tar.Header{
			Name:     top + "/" + name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(body)),
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchExtractsFiltersAndCaches(t *testing.T) {
	payload := makeTarGz(t, "clew-abc1234", map[string]string{
		"pkg/mod.py":   "def hello():\n    return 1\n",
		"README.md":    "# clew\n",
		"bin/blob.png": "not text",
		"big.py":       strings.Repeat("x", MaxFileSize+10),
	})

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/cyokozai/clew/tar.gz/abc1234" {
			t.Errorf("想定外のパス: %s", r.URL.Path)
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })

	cache := t.TempDir()
	dir, err := Fetch(context.Background(), "cyokozai", "clew", "abc1234", cache)
	if err != nil {
		t.Fatalf("Fetch が失敗: %v", err)
	}
	if filepath.Base(dir) != "cyokozai-clew-abc1234" {
		t.Errorf("展開先 = %s", dir)
	}

	if b, err := os.ReadFile(filepath.Join(dir, "pkg", "mod.py")); err != nil {
		t.Errorf("トップレベルを剥がせていない: %v", err)
	} else if !strings.Contains(string(b), "def hello") {
		t.Errorf("中身が違う: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("README.md が無い: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "blob.png")); err == nil {
		t.Error("許可リスト外の拡張子が残っている")
	}
	if _, err := os.Stat(filepath.Join(dir, "big.py")); err == nil {
		t.Error("1 MB 超のファイルが残っている")
	}

	// 2 回目はキャッシュを使い、取得しない。
	dir2, err := Fetch(context.Background(), "cyokozai", "clew", "abc1234", cache)
	if err != nil {
		t.Fatalf("2 回目の Fetch が失敗: %v", err)
	}
	if dir2 != dir {
		t.Errorf("展開先が変わった: %s != %s", dir2, dir)
	}
	if hits != 1 {
		t.Errorf("取得回数 = %d, want 1", hits)
	}
}

func TestFetchReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old })

	if _, err := Fetch(context.Background(), "x", "y", "deadbeef", t.TempDir()); err == nil {
		t.Fatal("エラーを返すはず")
	}
}

func TestStripTopLevel(t *testing.T) {
	cases := map[string]string{
		"repo-sha/a/b.py": "a/b.py",
		"repo-sha/x.py":   "x.py",
		"repo-sha":        "",
	}
	for in, want := range cases {
		if got := stripTopLevel(in); got != want {
			t.Errorf("stripTopLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeRelRejectsEscape(t *testing.T) {
	if safeRel("../outside.py") {
		t.Error("書庫の外へ出るパスを通している")
	}
	if !safeRel("pkg/mod.py") {
		t.Error("正しい相対パスを弾いている")
	}
}
