package judge

// このファイルは判定の記録（費用の見積もりと実測、永続キャッシュ）を持つ。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Cost は実行前の見積もり。
type Cost struct {
	InputTokens int
	USD         float64
	Neurons     float64
}

// Estimate は本体の JSON の長さから入力トークンを概算し（4 バイトで 1 トークン）、費用を見積もる。
// 判定器側の指示文の分は含まないので、実測（Usage）より小さく出ることがある。
func Estimate(state any, questions map[string]Question) (Cost, error) {
	b, err := json.Marshal(requestBody{Model: Model, State: state, Questions: questions})
	if err != nil {
		return Cost{}, err
	}
	tok := (len(b) + 3) / 4
	return Cost{
		InputTokens: tok,
		USD:         float64(tok) * InputUSDPerMtok / 1e6,
		Neurons:     float64(tok) * NeuronsPerMtok / 1e6,
	}, nil
}

// Meter は実測の積算。複数の goroutine から使ってよい。
type Meter struct {
	mu sync.Mutex
	s  Snapshot
}

// Snapshot は Meter のある時点の値。
type Snapshot struct {
	// Requests は HTTP で判定させた回数（キャッシュ命中は含まない）。
	Requests     int
	CacheHits    int
	InputTokens  int
	OutputTokens int
}

// USD は入力トークンから求めた実費（出力は料金の記載が無いので 0）。
func (s Snapshot) USD() float64 { return float64(s.InputTokens) * InputUSDPerMtok / 1e6 }

// Neurons は入力トークンから求めた Neurons の消費。
func (s Snapshot) Neurons() float64 { return float64(s.InputTokens) * NeuronsPerMtok / 1e6 }

// Add は 1 回分の usage を積む。
func (m *Meter) Add(u Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.Requests++
	m.s.InputTokens += u.InputTokens
	m.s.OutputTokens += u.OutputTokens
}

func (m *Meter) hit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.CacheHits++
}

// Snapshot は今の積算を返す。
func (m *Meter) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s
}

// Cache は判定結果の永続キャッシュ。鍵は正規化した本体の SHA-256。
type Cache struct {
	db *sql.DB
}

// DefaultCachePath は ~/.clew/judge/cache.db。
func DefaultCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".clew", "judge", "cache.db"), nil
}

// OpenCache はキャッシュを開く。無ければ作る。
func OpenCache(path string) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS answers (key TEXT PRIMARY KEY, body BLOB NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("judge: キャッシュの表を作れない: %w", err)
	}
	return &Cache{db: db}, nil
}

func (c *Cache) get(key string) (*Response, bool, error) {
	var b []byte
	err := c.db.QueryRow(`SELECT body FROM answers WHERE key = ?`, key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var r Response
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, false, err
	}
	r.Cached = true
	return &r, true, nil
}

func (c *Cache) put(key string, r *Response) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`INSERT OR REPLACE INTO answers (key, body, created_at) VALUES (?, ?, ?)`, key, b, time.Now().Unix())
	return err
}

// Close はキャッシュを閉じる。
func (c *Cache) Close() error { return c.db.Close() }
