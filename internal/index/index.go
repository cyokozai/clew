// Package index は FTS5 の全文索引を作り、1 ファイル 1 行で保持する。
package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Index は 1 つの sha に対応する SQLite 索引。
type Index struct {
	db *sql.DB
	// Path は索引ファイルのパス。
	Path string
	// Fresh は今回新しく作られた索引かどうか。既存を開いたときは false。
	Fresh bool
}

// Root は索引の置き場。既定は ~/.clew/index。
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".clew", "index"), nil
}

// PathFor は <owner>-<repo>-<sha>.db の位置を返す。
func PathFor(root, owner, repoName, sha string) (string, error) {
	if root == "" {
		r, err := Root()
		if err != nil {
			return "", err
		}
		root = r
	}
	return filepath.Join(root, fmt.Sprintf("%s-%s-%s.db", owner, repoName, sha)), nil
}

// Open は索引を開く。無ければ作る。既にあるものは作り直さない。
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	fresh := true
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		fresh = false
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// pragma は接続ごとに効くので、接続を 1 本に固定する。
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode=MEMORY", "PRAGMA synchronous=OFF"} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("index: %s に失敗: %w", pragma, err)
		}
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS files USING fts5(path, body, tokenize='unicode61 remove_diacritics 2')`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("index: 仮想表を作れない: %w", err)
	}
	return &Index{db: db, Path: path, Fresh: fresh}, nil
}

// DB は検索のために内部のハンドルを渡す。
func (i *Index) DB() *sql.DB { return i.db }

// AddFile は 1 ファイルを 1 行として入れる。
func (i *Index) AddFile(path, body string) error {
	_, err := i.db.Exec(`INSERT INTO files(path, body) VALUES(?, ?)`, path, body)
	return err
}

// Count は入っている行数を返す。
func (i *Index) Count() (int, error) {
	var n int
	err := i.db.QueryRow(`SELECT count(*) FROM files`).Scan(&n)
	return n, err
}

// Close は索引を閉じる。
func (i *Index) Close() error { return i.db.Close() }
