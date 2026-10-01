// clew は GitHub の Issue から、関係するソースコードの位置を特定して URL で返す。
// v0 はニューラルモデルを使わず、BM25 だけで位置を当てる。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/cyokozai/clew/internal/eval"
	"github.com/cyokozai/clew/internal/retrieve"
	"github.com/cyokozai/clew/internal/swebench"
)

type CLI struct {
	Index  IndexCmd  `cmd:"" help:"リポジトリの索引を作る"`
	Locate LocateCmd `cmd:"" help:"Issue から関係するソースの位置を返す"`
	Eval   EvalCmd   `cmd:"" help:"SWE-bench で位置特定の正解率を測る"`
}

type IndexCmd struct {
	Repo string `arg:"" help:"owner/repo"`
	SHA  string `name:"sha" required:"" help:"索引を作る commit の sha"`
}

func (c *IndexCmd) Run() error {
	ctx := context.Background()
	idx, err := eval.Prepare(ctx, c.Repo, c.SHA)
	if err != nil {
		return err
	}
	defer idx.Close()
	n, err := idx.Count()
	if err != nil {
		return err
	}
	fmt.Printf("索引を用意した: %s (%d ファイル)\n", idx.Path, n)
	return nil
}

type LocateCmd struct {
	Repo   string `arg:"" help:"owner/repo"`
	Number int    `arg:"" help:"Issue 番号"`
	SHA    string `name:"sha" help:"位置を測る commit の sha（省略時は既定ブランチの先端）"`
	K      int    `name:"k" default:"10" help:"出す件数"`
}

func (c *LocateCmd) Run() error {
	ctx := context.Background()
	sha := c.SHA
	if sha == "" {
		fmt.Fprintln(os.Stderr, "既定ブランチの先端を調べている...")
		s, err := headSHA(ctx, c.Repo)
		if err != nil {
			return err
		}
		sha = s
	}

	fmt.Fprintf(os.Stderr, "Issue #%d を取得している...\n", c.Number)
	body, err := issueBody(ctx, c.Repo, c.Number)
	if err != nil {
		return err
	}

	idx, err := eval.Prepare(ctx, c.Repo, sha)
	if err != nil {
		return err
	}
	defer idx.Close()

	hits, err := retrieve.Search(idx, body, c.K)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Println("該当なし")
		return nil
	}
	for _, h := range hits {
		fmt.Printf("%.5f  %s\n", h.Score, h.Path)
		fmt.Printf("         https://github.com/%s/blob/%s/%s\n", c.Repo, sha, h.Path)
	}
	return nil
}

type EvalCmd struct {
	Dataset  string `name:"dataset" default:"princeton-nlp/SWE-bench_Verified" help:"評価に使うデータセット"`
	Limit    int    `name:"limit" default:"5" help:"回すインスタンス数（0 で全件）"`
	GoldSide string `name:"gold-side" default:"a" enum:"a,b" help:"正解ファイルを diff の a 側と b 側のどちらで取るか"`
}

func (c *EvalCmd) Run() error {
	ctx := context.Background()
	fmt.Fprintf(os.Stderr, "%s を取得している (limit=%d)...\n", c.Dataset, c.Limit)
	instances, err := swebench.Load(ctx, c.Dataset, c.Limit)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d 件を取得した\n", len(instances))

	start := time.Now()
	res, err := eval.Run(ctx, instances, []int{1, 5, 10}, c.GoldSide)
	if err != nil {
		return err
	}

	fmt.Printf("\ndataset=%s  N=%d  gold-side=%s\n", c.Dataset, res.N, c.GoldSide)
	fmt.Println("k    file recall@k   acc@k")
	for _, k := range []int{1, 5, 10} {
		fmt.Printf("%-4d %-14.3f %.3f\n", k, res.FileRecallAt[k], res.AccAt[k])
	}
	fmt.Printf("検索の応答時間（中央値）: %v\n", res.MedianLatency)
	fmt.Printf("総実行時間: %v\n", time.Since(start).Round(time.Second))
	return nil
}

var githubAPI = "https://api.github.com"

func githubJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clew: GitHub API が %s を返した (%s)", resp.Status, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func issueBody(ctx context.Context, repoFull string, number int) (string, error) {
	var issue struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := githubJSON(ctx, fmt.Sprintf("/repos/%s/issues/%d", repoFull, number), &issue); err != nil {
		return "", err
	}
	return strings.TrimSpace(issue.Title + "\n\n" + issue.Body), nil
}

func headSHA(ctx context.Context, repoFull string) (string, error) {
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := githubJSON(ctx, "/repos/"+repoFull, &info); err != nil {
		return "", err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := githubJSON(ctx, fmt.Sprintf("/repos/%s/commits/%s", repoFull, info.DefaultBranch), &commit); err != nil {
		return "", err
	}
	return commit.SHA, nil
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("clew"),
		kong.Description("Issue から関係するソースコードの位置を特定して URL で返す"),
		kong.UsageOnError(),
	)
	ctx.FatalIfErrorf(ctx.Run())
}
