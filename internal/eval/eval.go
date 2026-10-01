// Package eval は SWE-bench のインスタンスに対して BM25 の位置特定を回し、指標を出す。
package eval

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cyokozai/clew/internal/index"
	"github.com/cyokozai/clew/internal/repo"
	"github.com/cyokozai/clew/internal/retrieve"
	"github.com/cyokozai/clew/internal/swebench"
)

// Logf は進捗の出力先。長走行の入口を無言にしないために使う。
var Logf = func(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// Result は評価の結果。
type Result struct {
	N int
	// FileRecallAt は正解ファイルのうち上位 k に現れた割合の平均。
	FileRecallAt map[int]float64
	// AccAt は正解ファイルが 1 つでも上位 k に現れた割合。
	AccAt         map[int]float64
	MedianLatency time.Duration
}

// Prepare は対象 sha のソースを取得し、索引を用意して返す。既にある索引は作り直さない。
func Prepare(ctx context.Context, repoFull, sha string) (*index.Index, error) {
	owner, name, ok := strings.Cut(repoFull, "/")
	if !ok {
		return nil, fmt.Errorf("eval: リポジトリ名は owner/repo の形で渡す: %q", repoFull)
	}

	idxPath, err := index.PathFor("", owner, name, sha)
	if err != nil {
		return nil, err
	}
	idx, err := index.Open(idxPath)
	if err != nil {
		return nil, err
	}
	if !idx.Fresh {
		if n, err := idx.Count(); err == nil && n > 0 {
			Logf("  索引は既にある (%d ファイル): %s", n, filepath.Base(idxPath))
			return idx, nil
		}
	}

	Logf("  ソースを取得中: %s@%.7s", repoFull, sha)
	dir, err := repo.Fetch(ctx, owner, name, sha, "")
	if err != nil {
		_ = idx.Close()
		return nil, err
	}

	n, err := indexDir(idx, dir)
	if err != nil {
		_ = idx.Close()
		return nil, err
	}
	Logf("  索引を作成: %d ファイル", n)
	return idx, nil
}

// indexDir は dir 以下のファイルを 1 ファイル 1 行で索引へ入れる。
func indexDir(idx *index.Index, dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".clew") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		if err := idx.AddFile(filepath.ToSlash(rel), string(body)); err != nil {
			return err
		}
		n++
		if n%500 == 0 {
			Logf("  索引中: %d ファイル", n)
		}
		return nil
	})
	return n, err
}

// Run はインスタンスごとに 取得 → 索引 → 検索 → 判定 を回す。
func Run(ctx context.Context, instances []swebench.Instance, k []int, goldSide string) (Result, error) {
	ks := append([]int(nil), k...)
	sort.Ints(ks)
	if len(ks) == 0 {
		ks = []int{1, 5, 10}
	}
	topK := ks[len(ks)-1]

	res := Result{FileRecallAt: map[int]float64{}, AccAt: map[int]float64{}}
	recallSum := map[int]float64{}
	accSum := map[int]float64{}
	var latencies []time.Duration

	for i, inst := range instances {
		Logf("[%d/%d] %s", i+1, len(instances), inst.InstanceID)

		gold := swebench.GoldFiles(inst.Patch).Side(goldSide)
		if len(gold) == 0 {
			Logf("  正解ファイルが取れないので飛ばす")
			continue
		}

		idx, err := Prepare(ctx, inst.Repo, inst.BaseCommit)
		if err != nil {
			return res, fmt.Errorf("%s: %w", inst.InstanceID, err)
		}

		start := time.Now()
		hits, err := retrieve.Search(idx, inst.ProblemStatement, topK)
		elapsed := time.Since(start)
		_ = idx.Close()
		if err != nil {
			return res, fmt.Errorf("%s: %w", inst.InstanceID, err)
		}
		latencies = append(latencies, elapsed)

		ranked := make([]string, 0, len(hits))
		for _, h := range hits {
			ranked = append(ranked, h.Path)
		}
		recall, acc := Score(gold, ranked, ks)
		for _, kk := range ks {
			recallSum[kk] += recall[kk]
			accSum[kk] += acc[kk]
		}
		res.N++

		Logf("  正解 %v / 上位 %d: %v (%.0f ms)", gold, topK, truncate(ranked, 3), float64(elapsed.Microseconds())/1000)
	}

	if res.N > 0 {
		for _, kk := range ks {
			res.FileRecallAt[kk] = recallSum[kk] / float64(res.N)
			res.AccAt[kk] = accSum[kk] / float64(res.N)
		}
	}
	res.MedianLatency = Median(latencies)
	return res, nil
}

// Score は 1 インスタンスの再現率と acc を k ごとに出す。
func Score(gold, ranked []string, ks []int) (recall map[int]float64, acc map[int]float64) {
	recall = map[int]float64{}
	acc = map[int]float64{}
	goldSet := map[string]bool{}
	for _, g := range gold {
		goldSet[g] = true
	}
	for _, k := range ks {
		top := ranked
		if len(top) > k {
			top = top[:k]
		}
		hit := 0
		counted := map[string]bool{}
		for _, p := range top {
			if goldSet[p] && !counted[p] {
				counted[p] = true
				hit++
			}
		}
		if len(goldSet) > 0 {
			recall[k] = float64(hit) / float64(len(goldSet))
		}
		if hit > 0 {
			acc[k] = 1
		}
	}
	return recall, acc
}

// Median は応答時間の中央値を返す。
func Median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

func truncate(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
