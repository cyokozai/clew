// Package judge は判定層（段 4）のクライアント。Cloudflare Workers AI の
// Clef-flash（@cf/cloudflare/clef-flash、Apache-2.0）へ型付きの質問を投げ、答えを受け取る。
//
// 本体の形は System One API と同じなので、封筒なしで答える自前のサーバーへも
// BaseURL を差し替えるだけで向けられる。
//
// ここで返す確率・得点はすべて判定器が返した生の値（較正前の値）であり、
// そのまま出力へ出してはならない。較正は保留集合に対して別に行う。
package judge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Model は本体の model 欄に入れる値。
const Model = "clef-flash"

// 料金と無料枠（2026-10 時点の公開値）。出力の料金は記載が無いので 0 として扱う。
const (
	// InputUSDPerMtok は入力 100 万トークンあたりの USD。
	InputUSDPerMtok = 0.09
	// NeuronsPerMtok は入力 100 万トークンあたりの Neurons。無料枠は Workers AI 全体で 10,000 Neurons/日。
	NeuronsPerMtok = 8182
	// FreeNeuronsPerDay は Workers AI 全体の無料枠。
	FreeNeuronsPerDay = 10000
)

// 質問の型。
const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"
)

// WorkersAIURL は Workers AI の REST 経由で Clef-flash を呼ぶ URL を返す。
func WorkersAIURL(accountID string) string {
	return "https://api.cloudflare.com/client/v4/accounts/" + accountID + "/ai/run/@cf/cloudflare/clef-flash"
}

// Question は 1 問。Noul / Choice / Score で組み立てる。
type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Noul は yes/no の問い。ifTrue / ifFalse が空なら criteria を付けない。
func Noul(instructions, ifTrue, ifFalse string) Question {
	q := Question{Type: TypeNoul, Instructions: instructions}
	if ifTrue != "" || ifFalse != "" {
		c := map[string]any{}
		if ifTrue != "" {
			c["true"] = ifTrue
		}
		if ifFalse != "" {
			c["false"] = ifFalse
		}
		q.Criteria = c
	}
	return q
}

// Choice は選択肢（2〜255 個、id → 説明）から 1 つを選ばせる問い。
func Choice(instructions string, options map[string]any) Question {
	return Question{Type: TypeChoice, Instructions: instructions, Criteria: options}
}

// Score は低い順に並べた段階（2〜10 段）で採点させる問い。
func Score(instructions string, levels ...any) Question {
	return Question{Type: TypeScore, Instructions: instructions, Criteria: levels}
}

// Answer は 1 問への答え。どの欄が埋まるかは Type で決まる。
// 数値はすべて較正前の値。
type Answer struct {
	Type string `json:"type"`
	// Noul は yes である確率（較正前）。
	Noul float64 `json:"noul,omitempty"`
	// Choice は最も確率の高い選択肢。
	Choice string `json:"choice,omitempty"`
	// Score は確率で重み付けした段階（較正前）。段階の間の値にもなる。
	Score float64 `json:"score,omitempty"`
	// Legend は段階の番号（文字列）→ 説明。
	Legend map[string]any `json:"legend,omitempty"`
	// Probabilities は選択肢・段階ごとの確率（較正前）。
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	// Confidence は確率から導いた確信度（較正前）。
	Confidence float64 `json:"confidence,omitempty"`
}

// Usage は 1 回の判定で使ったトークン数。
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response は判定の結果。Answers は質問と同じ id で引く。
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
	// Cached はキャッシュから返したかどうか。命中時は HTTP を出していない。
	Cached bool `json:"-"`
}

type requestBody struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// StatusError は再試行しない（または再試行し尽くした）HTTP の失敗。
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("judge: HTTP %d: %s", e.Code, e.Body)
}

// Client は判定器への接続。ゼロ値ではなく New / FromEnv で作る。
type Client struct {
	// BaseURL は POST 先の完全な URL。
	BaseURL string
	// Token は Bearer トークン。空なら Authorization を付けない。
	Token string
	HTTP  *http.Client
	// MaxRetries は初回の後に再試行する回数。
	MaxRetries int
	// Backoff は最初の待ち時間。再試行ごとに倍にする。
	Backoff time.Duration
	// Cache が nil でなければ、同じ本体を二度送らない。
	Cache *Cache
	// Meter が nil でなければ、HTTP で得た usage を積算する。
	Meter *Meter
}

// New は baseURL へ token で話すクライアントを作る。
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:    baseURL,
		Token:      token,
		HTTP:       &http.Client{Timeout: 2 * time.Minute},
		MaxRetries: 5,
		Backoff:    time.Second,
	}
}

// FromEnv は CLOUDFLARE_ACCOUNT_ID と CLOUDFLARE_API_TOKEN から Workers AI 向けのクライアントを作る。
func FromEnv() (*Client, error) {
	acc, tok := os.Getenv("CLOUDFLARE_ACCOUNT_ID"), os.Getenv("CLOUDFLARE_API_TOKEN")
	if acc == "" || tok == "" {
		return nil, errors.New("judge: CLOUDFLARE_ACCOUNT_ID と CLOUDFLARE_API_TOKEN の両方が要る")
	}
	return New(WorkersAIURL(acc), tok), nil
}

func validate(questions map[string]Question) error {
	if n := len(questions); n < 1 || n > 64 {
		return fmt.Errorf("judge: 質問は 1〜64 問（%d 問）", n)
	}
	return nil
}

// Ask は state（文字列または構造データ）について questions を判定させる。
// 返す確率・得点は較正前の値。
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	if err := validate(questions); err != nil {
		return nil, err
	}
	req := requestBody{Model: Model, State: state, Questions: questions}
	key, err := cacheKey(req)
	if err != nil {
		return nil, err
	}
	if c.Cache != nil {
		if r, ok, err := c.Cache.get(key); err != nil {
			return nil, err
		} else if ok {
			if c.Meter != nil {
				c.Meter.hit()
			}
			return r, nil
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	r, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if c.Meter != nil {
		c.Meter.Add(r.Usage)
	}
	if c.Cache != nil {
		if err := c.Cache.put(key, r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// post は再試行つきで本体を送り、成功した応答の中身を返す。
// transport 例外（接続断・タイムアウト）と 429 / 5xx は指数的に待って再試行し、
// それ以外の 4xx はすぐに返す。
func (c *Client) post(ctx context.Context, body []byte) ([]byte, error) {
	var last error
	for attempt := 0; ; attempt++ {
		raw, wait, err := c.once(ctx, body)
		if err == nil {
			return raw, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var se *StatusError
		if errors.As(err, &se) && se.Code != http.StatusTooManyRequests && se.Code < 500 {
			return nil, err
		}
		last = err
		if attempt >= c.MaxRetries {
			return nil, fmt.Errorf("judge: %d 回試して失敗: %w", attempt+1, last)
		}
		if wait == 0 {
			wait = c.Backoff << attempt
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// once は 1 回だけ送る。wait は Retry-After があればその秒数。
func (c *Client) once(ctx context.Context, body []byte) ([]byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode/100 != 2 {
		var wait time.Duration
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s <= 60 {
			wait = time.Duration(s) * time.Second
		}
		return nil, wait, &StatusError{Code: resp.StatusCode, Body: snippet(raw)}
	}
	return raw, 0, nil
}

func snippet(b []byte) string {
	if len(b) > 512 {
		return string(b[:512]) + "…"
	}
	return string(b)
}

// decode は Workers AI の封筒 {"result":…,"success":…} があれば剥がし、無ければそのまま読む。
func decode(raw []byte) (*Response, error) {
	var env struct {
		Result  json.RawMessage `json:"result"`
		Success *bool           `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("judge: 応答を読めない: %w", err)
	}
	if env.Success != nil {
		if !*env.Success {
			return nil, fmt.Errorf("judge: success=false: %+v", env.Errors)
		}
		raw = env.Result
	}
	var r Response
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("judge: 本体を読めない: %w", err)
	}
	if r.Answers == nil {
		return nil, fmt.Errorf("judge: answers が無い: %s", snippet(raw))
	}
	return &r, nil
}

// canonical は map の並びに依らない JSON を返す。
// 一度 any へ戻してから書き直すと、encoding/json が全段の鍵を整列する。
func canonical(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	return json.Marshal(x)
}

func cacheKey(req requestBody) (string, error) {
	b, err := canonical(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
