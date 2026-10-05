package judge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 封筒なしの本体（System One API と同形）。3 種の質問型を 1 本に入れる。
const bareBody = `{
  "model": "clef-flash",
  "answers": {
    "relevant": {"type": "noul", "noul": 0.83},
    "kind": {"type": "choice", "choice": "bug", "probabilities": {"bug": 0.7, "feature": 0.3}, "confidence": 0.4},
    "fit": {"type": "score", "score": 1.6, "legend": {"0": "無関係", "1": "周辺", "2": "原因"}, "probabilities": {"0": 0.1, "1": 0.2, "2": 0.7}, "confidence": 0.5}
  },
  "usage": {"input_tokens": 1200, "output_tokens": 9}
}`

func enveloped(body string) string {
	return `{"result":` + body + `,"success":true,"errors":[],"messages":[]}`
}

func questions() map[string]Question {
	return map[string]Question{
		"relevant": Noul("この関数は Issue の原因か", "原因である", "原因ではない"),
		"kind":     Choice("Issue の種類", map[string]any{"bug": "不具合", "feature": "機能追加"}),
		"fit":      Score("コード片と Issue の関係", "無関係", "周辺", "原因"),
	}
}

func newTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c := New(url, "test-token")
	c.Backoff = time.Millisecond
	return c
}

func checkAnswers(t *testing.T, r *Response) {
	t.Helper()
	if r.Model != "clef-flash" {
		t.Errorf("Model = %q", r.Model)
	}
	if a := r.Answers["relevant"]; a.Type != TypeNoul || a.Noul != 0.83 {
		t.Errorf("noul = %+v", a)
	}
	if a := r.Answers["kind"]; a.Type != TypeChoice || a.Choice != "bug" || a.Probabilities["feature"] != 0.3 || a.Confidence != 0.4 {
		t.Errorf("choice = %+v", a)
	}
	if a := r.Answers["fit"]; a.Type != TypeScore || a.Score != 1.6 || a.Legend["2"] != "原因" || a.Probabilities["2"] != 0.7 || a.Confidence != 0.5 {
		t.Errorf("score = %+v", a)
	}
	if r.Usage.InputTokens != 1200 || r.Usage.OutputTokens != 9 {
		t.Errorf("usage = %+v", r.Usage)
	}
}

func TestAskSendsTypedRequestAndReadsEnvelope(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if h := r.Header.Get("Authorization"); h != "Bearer test-token" {
			t.Errorf("Authorization = %q", h)
		}
		if h := r.Header.Get("Content-Type"); h != "application/json" {
			t.Errorf("Content-Type = %q", h)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, enveloped(bareBody))
	}))
	defer srv.Close()

	r, err := newTestClient(t, srv.URL).Ask(context.Background(), "Issue 本文とコード片", questions())
	if err != nil {
		t.Fatal(err)
	}
	checkAnswers(t, r)

	if got["model"] != "clef-flash" || got["state"] != "Issue 本文とコード片" {
		t.Errorf("request = %v", got)
	}
	qs := got["questions"].(map[string]any)
	noul := qs["relevant"].(map[string]any)
	if noul["type"] != "noul" || noul["criteria"].(map[string]any)["true"] != "原因である" {
		t.Errorf("noul question = %v", noul)
	}
	choice := qs["kind"].(map[string]any)
	if choice["type"] != "choice" || choice["criteria"].(map[string]any)["bug"] != "不具合" {
		t.Errorf("choice question = %v", choice)
	}
	score := qs["fit"].(map[string]any)
	if lv := score["criteria"].([]any); score["type"] != "score" || len(lv) != 3 || lv[0] != "無関係" {
		t.Errorf("score question = %v", score)
	}
}

func TestAskReadsBareBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, bareBody)
	}))
	defer srv.Close()

	r, err := newTestClient(t, srv.URL).Ask(context.Background(), map[string]any{"issue": "x"}, questions())
	if err != nil {
		t.Fatal(err)
	}
	checkAnswers(t, r)
}

func TestAskEnvelopeFailureIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":null,"success":false,"errors":[{"code":5006,"message":"bad input"}],"messages":[]}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Ask(context.Background(), "s", questions())
	if err == nil || !strings.Contains(err.Error(), "bad input") {
		t.Fatalf("err = %v", err)
	}
}

func TestAskRetries429And5xxThenSucceeds(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = io.WriteString(w, enveloped(bareBody))
		}
	}))
	defer srv.Close()

	r, err := newTestClient(t, srv.URL).Ask(context.Background(), "s", questions())
	if err != nil {
		t.Fatal(err)
	}
	checkAnswers(t, r)
	if n.Load() != 3 {
		t.Errorf("要求回数 = %d, want 3", n.Load())
	}
}

func TestAskRetriesTransportError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			// 応答を返さずに接続を切る。
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, bareBody)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).Ask(context.Background(), "s", questions()); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Errorf("要求回数 = %d, want 2", n.Load())
	}
}

func TestAskRetriesTimeout(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			time.Sleep(200 * time.Millisecond)
		}
		_, _ = io.WriteString(w, bareBody)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
	if _, err := c.Ask(context.Background(), "s", questions()); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Errorf("要求回数 = %d, want 2", n.Load())
	}
}

func TestAskGivesUpAfterMaxRetries(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	c.MaxRetries = 2
	_, err := c.Ask(context.Background(), "s", questions())
	if err == nil {
		t.Fatal("err = nil")
	}
	if n.Load() != 3 {
		t.Errorf("要求回数 = %d, want 3（初回 + 再試行 2）", n.Load())
	}
}

func TestAskClientErrorIsImmediate(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errors":[{"message":"questions must not be empty"}]}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv.URL).Ask(context.Background(), "s", questions())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest || !strings.Contains(se.Error(), "questions must not be empty") {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 1 {
		t.Errorf("要求回数 = %d, want 1", n.Load())
	}
}

func TestAskValidatesQuestionCount(t *testing.T) {
	c := New("http://127.0.0.1:1", "t")
	if _, err := c.Ask(context.Background(), "s", nil); err == nil {
		t.Error("0 問で err = nil")
	}
	many := map[string]Question{}
	for i := 0; i < 65; i++ {
		many[string(rune('a'+i%26))+strings.Repeat("x", i)] = Noul("q", "", "")
	}
	if _, err := c.Ask(context.Background(), "s", many); err == nil {
		t.Error("65 問で err = nil")
	}
}

func TestCacheHitSkipsHTTP(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.WriteString(w, enveloped(bareBody))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "judge", "cache.db")
	cache, err := OpenCache(path)
	if err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, srv.URL)
	c.Cache = cache
	c.Meter = &Meter{}

	state := map[string]any{"issue": "落ちる", "code": "func f() {}"}
	first, err := c.Ask(context.Background(), state, questions())
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached {
		t.Error("初回が Cached")
	}
	// 同じ内容を別の map として組み直しても命中する（正規化の確認）。
	again := map[string]any{"code": "func f() {}", "issue": "落ちる"}
	second, err := c.Ask(context.Background(), again, questions())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached {
		t.Error("2 回目が Cached でない")
	}
	checkAnswers(t, second)
	if n.Load() != 1 {
		t.Errorf("要求回数 = %d, want 1", n.Load())
	}
	_ = cache.Close()

	// 開き直しても残っている（永続）。
	cache, err = OpenCache(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	c.Cache = cache
	if r, err := c.Ask(context.Background(), state, questions()); err != nil || !r.Cached {
		t.Fatalf("開き直し後: r=%+v err=%v", r, err)
	}
	if n.Load() != 1 {
		t.Errorf("開き直し後の要求回数 = %d, want 1", n.Load())
	}

	// 中身が違えば外れる。
	if r, err := c.Ask(context.Background(), "別のコード片", questions()); err != nil || r.Cached {
		t.Fatalf("別入力: r=%+v err=%v", r, err)
	}
	if n.Load() != 2 {
		t.Errorf("別入力の要求回数 = %d, want 2", n.Load())
	}

	s := c.Meter.Snapshot()
	if s.Requests != 2 || s.CacheHits != 2 || s.InputTokens != 2400 || s.OutputTokens != 18 {
		t.Errorf("meter = %+v", s)
	}
}

func TestCacheKeyIgnoresMapOrder(t *testing.T) {
	a, err := cacheKey(requestBody{Model: "clef-flash", State: map[string]any{"a": 1, "b": []any{map[string]any{"y": 1, "x": 2}}}, Questions: questions()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := cacheKey(requestBody{Model: "clef-flash", State: map[string]any{"b": []any{map[string]any{"x": 2, "y": 1}}, "a": 1}, Questions: questions()})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("鍵が一致しない: %s != %s", a, b)
	}
}

func TestDefaultCachePath(t *testing.T) {
	p, err := DefaultCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join(".clew", "judge", "cache.db")) {
		t.Errorf("path = %s", p)
	}
}

func TestEstimate(t *testing.T) {
	state := strings.Repeat("a", 4000)
	e, err := Estimate(state, questions())
	if err != nil {
		t.Fatal(err)
	}
	// 本体の JSON はおよそ 4 バイト 1 トークンで数える。状態の 4000 バイトだけで 1000 トークンを超える。
	if e.InputTokens < 1000 || e.InputTokens > 1300 {
		t.Errorf("InputTokens = %d", e.InputTokens)
	}
	want := float64(e.InputTokens) * InputUSDPerMtok / 1e6
	if math.Abs(e.USD-want) > 1e-12 {
		t.Errorf("USD = %g, want %g", e.USD, want)
	}
	if math.Abs(e.Neurons-float64(e.InputTokens)*NeuronsPerMtok/1e6) > 1e-9 {
		t.Errorf("Neurons = %g", e.Neurons)
	}
}

func TestMeterUSD(t *testing.T) {
	m := &Meter{}
	m.Add(Usage{InputTokens: 1_000_000, OutputTokens: 50})
	m.Add(Usage{InputTokens: 500_000, OutputTokens: 50})
	s := m.Snapshot()
	if s.Requests != 2 || s.InputTokens != 1_500_000 || s.OutputTokens != 100 {
		t.Errorf("snapshot = %+v", s)
	}
	if math.Abs(s.USD()-0.135) > 1e-12 {
		t.Errorf("USD = %g, want 0.135", s.USD())
	}
	if math.Abs(s.Neurons()-1.5*NeuronsPerMtok) > 1e-9 {
		t.Errorf("Neurons = %g", s.Neurons())
	}
}

func TestWorkersAIURL(t *testing.T) {
	got := WorkersAIURL("abc123")
	want := "https://api.cloudflare.com/client/v4/accounts/abc123/ai/run/@cf/cloudflare/clef-flash"
	if got != want {
		t.Errorf("got %s", got)
	}
}

func TestFromEnvRequiresBoth(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	t.Setenv("CLOUDFLARE_API_TOKEN", "x")
	if _, err := FromEnv(); err == nil {
		t.Error("ACCOUNT_ID 無しで err = nil")
	}
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "acc")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != WorkersAIURL("acc") || c.Token != "x" {
		t.Errorf("client = %+v", c)
	}
}

// 実接続。CLEW_LIVE=1 と両方の環境変数があるときだけ走る。
func TestLiveClefFlash(t *testing.T) {
	if os.Getenv("CLEW_LIVE") != "1" || os.Getenv("CLOUDFLARE_ACCOUNT_ID") == "" || os.Getenv("CLOUDFLARE_API_TOKEN") == "" {
		t.Skip("CLEW_LIVE=1 と CLOUDFLARE_ACCOUNT_ID / CLOUDFLARE_API_TOKEN が要る")
	}
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	c.Meter = &Meter{}
	r, err := c.Ask(context.Background(), "Issue: nil を渡すと panic する。\nコード: func f(p *T) { p.x = 1 }", questions())
	if err != nil {
		t.Fatal(err)
	}
	for id := range questions() {
		if _, ok := r.Answers[id]; !ok {
			t.Errorf("答えが無い: %s", id)
		}
	}
	s := c.Meter.Snapshot()
	t.Logf("usage=%+v usd=%g neurons=%g", r.Usage, s.USD(), s.Neurons())
}
