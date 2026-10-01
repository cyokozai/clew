# clew の作法

**道具として読めること**が優先する。大規模開発の作法（層・ディレクトリ・抽象）は持ち込まない。

1. **パッケージを増やさない。`internal/` の下に 1 つの関心ごとに 1 パッケージ、1 パッケージ 1 ファイル。**
   400 行を超えたら 2 分割まで（3 分割はしない）。`cmd/clew/main.go` が唯一の入口。
2. **テストは実装と 1 対 1。** `internal/<pkg>/<pkg>_test.go` 1 本だけ。
   **テストはネットワークへ出ない。** `compose.yaml` の test サービスは `network_mode: none` で走る。実接続が要るものは `httptest` の fixture にする。
3. **新しい抽象（インターフェース・ラッパー）は 2 つ目の実装が来るまで作らない。**
4. **探針・一回限りの調査コードはコミットしない。** 測った結果は docs か PR 本文へ。
5. **索引と判定の結果はキャッシュに残す。** 同じ (Issue, コード片) を二度課金しない。

## 回し方

ローカルへは入れず、常にコンテナの中で動かす。実接続は `CLEW_LIVE=1` のときだけ。

```bash
docker compose run --rm test                       # network_mode: none
docker compose run --rm vet
docker compose run --rm clew locate <owner/repo> <issue-number>
docker compose run --rm clew eval --limit 20
docker compose run --rm tidy                       # go.sum の更新（ネットワーク要）
```

依存は `modernc.org/sqlite`（純 Go、FTS5 と sqlite-vec 同梱）と `github.com/alecthomas/kong` だけ。
増やすときは「標準ライブラリで書くと何行になるか」を先に見積もる。

## 外部サービス

| 用途 | 既定 | 鍵 | 備考 |
|---|---|---|---|
| 再順位 | Qwen3-Reranker（Apache-2.0、ローカル） | 不要 | SweRank は cc-by-nc-4.0 なので同梱しない。受け口だけ用意する |
| 判定 | Laya / Kev（Apache-2.0、ローカル） | 不要 | `POST /v1/systemone` を話すものは全て差し替え可能 |
| 判定（商用） | Jev / typesafe.ai | `TYPESAFE_API_KEY` | 入力 $0.042/Mtok、出力無料。レート上限は資料間で食い違うので実測で確かめる |
| Issue とコードの取得 | GitHub API | `GITHUB_TOKEN` | 5,000 要求/時 |

- **判定器を 1 つに固定しない。** 既定はローカルで動く Apache-2.0 のものにし、商用 API は選択肢の 1 つとして置く。
- **較正は自前で持つ。** 判定器が返す生の確率をそのまま出さない。保留集合に対して較正し直したものだけを出力する。判定器を替えたら較正もやり直す。
- **従量課金の経路を使うときは**、実行の前に要求数と概算の費用を見積もり、実行の後に実測を記録する。
- 接続の切断は必ず起きる。transport 例外も再試行の対象にし、結果は永続キャッシュへ残す。
- 長走行の入口には進捗の出力を置く。無言のまま数分待たせない。

## git と PR

worktree の作業ブランチへの `git push` と `gh pr create` / `gh pr edit` / `gh pr close` は確認なしで行う。
force push は権限の `deny`、main への push と merge / rebase はフックが拒否する。merge は人が GitHub 上で行う。

## 日本語の語彙

地の文（コメント・コミットメッセージを含む）では次の語を使う。言い換え前の語は使わない。

- **谷**（初出のみ「谷（basin of attraction）」と英語併記可）
- **計算量** / **実行時間** / **評価回数**（文脈で使い分ける）
- **停滞**（「境界際」）
- **総実行時間**（wall-clock は原則使わない）
- **更新則**（候補解の生成規則。初出で定義する）

ファイル名・識別子はそのまま。英語で書くときも valley / computation time / update rule で揃える。
