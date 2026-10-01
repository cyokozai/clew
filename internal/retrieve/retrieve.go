// Package retrieve は Issue 本文から問合せを作り、BM25 で候補ファイルを並べる。
package retrieve

import (
	"regexp"
	"sort"
	"strings"

	"github.com/cyokozai/clew/internal/index"
)

// Hit は 1 件の候補。Score は大きいほど関連が強い（RRF の和）。
type Hit struct {
	Path  string
	Score float64
}

// rrfK は Reciprocal Rank Fusion の定数。
const rrfK = 60.0

// maxTerms は 1 本の問合せに入れる語数の上限。
const (
	maxProseTerms = 60
	maxCodeTerms  = 40
)

var (
	reBacktick = regexp.MustCompile("`([^`\n]+)`")
	reCamel    = regexp.MustCompile(`[A-Za-z]+[a-z0-9]*(?:[A-Z][a-z0-9]+)+`)
	reSnake    = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+`)
	rePath     = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:py|go|js|ts|java|rb|rs|c|h|cpp|md|txt|ya?ml|json|toml)\b`)
	reErrName  = regexp.MustCompile(`[A-Z][A-Za-z0-9]*(?:Error|Exception|Warning)`)
	reWord     = regexp.MustCompile(`[A-Za-z0-9_]+`)
)

// stopwords は内容語を選ぶときに落とす語。
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "that": true, "this": true,
	"with": true, "but": true, "not": true, "are": true, "was": true,
	"have": true, "has": true, "from": true, "you": true, "its": true,
	"it's": true, "can": true, "should": true, "would": true, "when": true,
	"then": true, "there": true, "here": true, "they": true, "them": true,
	"all": true, "any": true, "out": true, "use": true, "using": true,
	"get": true, "got": true, "see": true, "also": true, "into": true,
	"what": true, "which": true, "while": true, "because": true,
	"code": true, "issue": true, "bug": true, "problem": true,
	"expected": true, "actual": true, "behavior": true, "description": true,
	"version": true, "python": true, "example": true, "following": true,
}

// Search は本文から 2 本の問合せを作り、結果を RRF で統合して上位 k 件を返す。
func Search(idx *index.Index, issueBody string, k int) ([]Hit, error) {
	if k <= 0 {
		k = 10
	}
	limit := k * 5
	if limit < 50 {
		limit = 50
	}

	queries := []string{
		buildQuery(proseTerms(issueBody)),
		buildQuery(codeTerms(issueBody)),
	}

	fused := map[string]float64{}
	order := []string{}
	for _, q := range queries {
		if q == "" {
			continue
		}
		paths, err := match(idx, q, limit)
		if err != nil {
			return nil, err
		}
		for rank, p := range paths {
			if _, ok := fused[p]; !ok {
				order = append(order, p)
			}
			fused[p] += 1.0 / (rrfK + float64(rank+1))
		}
	}

	hits := make([]Hit, 0, len(fused))
	for _, p := range order {
		hits = append(hits, Hit{Path: p, Score: fused[p]})
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].Score > hits[b].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// match は 1 本の問合せを BM25 の昇順（小さいほど関連が強い）で引く。
func match(idx *index.Index, query string, limit int) ([]string, error) {
	rows, err := idx.DB().Query(
		`SELECT path, bm25(files, 1.0, 1.0) AS rank FROM files WHERE files MATCH ? ORDER BY rank LIMIT ?`,
		query, limit)
	if err != nil {
		// 問合せが FTS5 の構文に合わなかった場合は、その問合せを飛ばす。
		return nil, nil
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var p string
		var score float64
		if err := rows.Scan(&p, &score); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// buildQuery は語を FTS5 の OR 連結にする。空なら空文字を返す。
func buildQuery(terms []string) string {
	clean := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, t := range terms {
		// FTS5 で特別な意味を持つ文字を落とし、bareword だけを残す。
		for _, w := range reWord.FindAllString(t, -1) {
			w = strings.ToLower(w)
			if len(w) < 3 || seen[w] {
				continue
			}
			seen[w] = true
			clean = append(clean, w)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, " OR ")
}

// proseTerms は本文全体から内容語を拾う。
func proseTerms(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range reWord.FindAllString(body, -1) {
		lw := strings.ToLower(w)
		if len(lw) < 3 || stopwords[lw] || seen[lw] {
			continue
		}
		seen[lw] = true
		out = append(out, lw)
		if len(out) >= maxProseTerms {
			break
		}
	}
	return out
}

// codeTerms はコード由来の語（バッククォート・識別子・パス・例外名）を拾う。
func codeTerms(body string) []string {
	var raw []string
	for _, m := range reBacktick.FindAllStringSubmatch(body, -1) {
		raw = append(raw, m[1])
	}
	raw = append(raw, rePath.FindAllString(body, -1)...)
	raw = append(raw, reErrName.FindAllString(body, -1)...)
	raw = append(raw, reCamel.FindAllString(body, -1)...)
	raw = append(raw, reSnake.FindAllString(body, -1)...)

	var out []string
	seen := map[string]bool{}
	for _, t := range raw {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= maxCodeTerms {
			break
		}
	}
	return out
}
