// Package repo は GitHub のソース書庫を取得して一時ディレクトリへ展開する。
package repo

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BaseURL は tar.gz の取得元。テストでは httptest の URL へ差し替える。
var BaseURL = "https://codeload.github.com"

// HTTPClient は取得に使う HTTP クライアント。
var HTTPClient = &http.Client{Timeout: 300 * time.Second}

// MaxFileSize を超えるファイルは索引に入れない。
const MaxFileSize = 1 << 20

// textExt は残す拡張子の許可リスト。
var textExt = map[string]bool{
	".py": true, ".go": true, ".js": true, ".ts": true, ".java": true,
	".rb": true, ".rs": true, ".c": true, ".h": true, ".cpp": true,
	".md": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".toml": true,
}

// completeMarker はキャッシュが完成していることを示す目印。
const completeMarker = ".clew-complete"

// CacheRoot は展開先の基点。既定は ~/.clew/repos。
func CacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".clew", "repos"), nil
}

// Fetch は <owner>/<repo> の sha を取得して展開し、展開先のパスを返す。
// 同じ sha が既にキャッシュにあるときは取得しない。
func Fetch(ctx context.Context, owner, repoName, sha, cacheRoot string) (string, error) {
	if cacheRoot == "" {
		r, err := CacheRoot()
		if err != nil {
			return "", err
		}
		cacheRoot = r
	}
	dest := filepath.Join(cacheRoot, fmt.Sprintf("%s-%s-%s", owner, repoName, sha))
	if _, err := os.Stat(filepath.Join(dest, completeMarker)); err == nil {
		return dest, nil
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", err
	}

	tmp, err := os.MkdirTemp(cacheRoot, "clew-tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if err := download(ctx, owner, repoName, sha, tmp); err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(tmp, completeMarker))
	if err != nil {
		return "", err
	}
	_ = f.Close()

	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func download(ctx context.Context, owner, repoName, sha, dest string) error {
	u := fmt.Sprintf("%s/%s/%s/tar.gz/%s", BaseURL, owner, repoName, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("repo: codeload が %s を返した (%s)", resp.Status, u)
	}
	return Extract(resp.Body, dest)
}

// Extract は tar.gz を読み、テキストファイルだけを dest へ展開する。
// 書庫のトップレベルのディレクトリ名は剥がす。
func Extract(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		rel := stripTopLevel(h.Name)
		if rel == "" || !safeRel(rel) {
			continue
		}
		if !textExt[strings.ToLower(path.Ext(rel))] {
			continue
		}
		if h.Size > MaxFileSize {
			continue
		}

		out := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, MaxFileSize+1)); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
}

func stripTopLevel(name string) string {
	name = strings.TrimPrefix(path.Clean(name), "./")
	i := strings.IndexByte(name, '/')
	if i < 0 {
		return ""
	}
	return name[i+1:]
}

// safeRel は書庫の外へ出る相対パスを弾く。
func safeRel(rel string) bool {
	if path.IsAbs(rel) {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
