# 技術選定

2026-10-02 時点。決めた理由と、決めなかったものを残す。

## 言語は Go

判定層を差し替え可能にした時点で、モデル推論は `POST /v1/systemone` の向こう側へ出た。埋め込みと再順位も同じ形にすると、clew 本体に残るのは索引・検索・グラフ探索・集合選択・CLI だけになる。これは全部 Go の領分で、Python を選ぶ理由（モデルをプロセス内で動かせる）はもう効かない。

代わりに効くのは配布である。この分野の既存ツール（Agentless、LocAgent、CoSIL）は全て Python の研究コードで、動かすのに環境構築が要る。単一バイナリで入る CLI は、それだけで差になる。

## 確定したもの

| 層 | 採ったもの | 理由 |
|---|---|---|
| 索引と全文検索 | `modernc.org/sqlite` | 純 Go。**FTS5 が既定で有効**でビルドタグ不要、`bm25()` が組み込み。sqlite-vec も同梱なので、全文検索とベクトル検索が同じ DB ファイルに同居する。BM25 を自分で書く必要も bleve を入れる必要も無い |
| CLI | `alecthomas/kong` | cobra は最終更新が 2026-07、open issue 455 件で停滞している。kong と urfave/cli v3 が活発 |
| SWE-bench | HuggingFace の行取得 API を直接叩く | parquet も Python も Docker も要らない。`/rows` に offset を進めて 5 回 GET すれば Verified が全件 JSON で取れる。正解ファイルは patch から出るので指標の計算は 100 行程度 |
| 推論サイドカー | infinity（MIT） | **TEI は Qwen3-Reranker を読めない**（classifier 型が非対応、issue #643 が open）。infinity は埋め込みと再順位を 1 プロセスで出せる。判定は Laya の CPU イメージが `/v1/systemone` をそのまま提供する |
| 判定器（2026-10-05） | Clef-flash を Workers AI（`@cf/cloudflare/clef-flash`）で呼ぶ | Apache-2.0 で重みが公開されており、System One 形式と同形の入出力を持つ。REST 1 本で呼べるので開発機に何も入れずに済む。入力 $0.09/Mtok、無料枠 10,000 Neurons/日（≒ 1.22 Mtok/日）。鍵は `CLOUDFLARE_API_TOKEN` と `CLOUDFLARE_ACCOUNT_ID`。Jev / TypeSafe は外した（`docs/design.md` の 2026-10-05 追記） |

依存は `modernc.org/sqlite` と `kong` の 2 つだけ。増やすときは「標準ライブラリで書くと何行になるか」を先に見積もる。

## 先送りにしたもの: tree-sitter と cgo

tree-sitter の Go バインディングは cgo 必須である。純 Go の再実装（`odvcencio/gotreesitter`、206 文法）は star こそ公式を上回るが、open issue 46 件に互換の不一致が並び、race の再現リポジトリも存在する。単一バイナリで配るという目的と、tree-sitter を使うという手段が、ここで正面からぶつかる。

**v0 には構文解析が要らない**ので、この判断は先送りした。依存グラフを作る段で、実データを見てから決める。cgo を採るなら、クロスコンパイルは 2 ジョブ構成になる（Linux は ubuntu ランナーで `aarch64-linux-gnu-gcc`、darwin は macOS ランナーで native。**zig cc は macOS SDK を同梱しないので darwin には使えない**）。

## 先送りにしたもの: 判定器の自前実行

Clef / Clef-flash は重みが公開されているので自前でも動かせるが、当面は Workers AI で呼び、自前実行は後日 GPU 機の Docker コンテナで行う。

- **HTTP サーバーが無い。** 公式が配るのは Python の `systemone()` 関数だけで、vLLM / SGLang は chat completions しか出さない。System One 形式を話すには、`systemone()` を包む小さな HTTP ラッパーを自分で書いてイメージに焼く必要がある。
- **開発機に載らない。** Clef-flash は BF16 で約 18 GB、Clef は H200 1 枚で試験されている。開発機は M3・16 GB で OrbStack に 8 GB を割いており、コンテナからは GPU も使えない。
- **clew 側の変更は接続先だけ。** 判定器は System One 形式の向こう側にあるので、Workers AI から自前実行へ移っても本体は替わらない。替えたら較正はやり直す。

## 配布

homebrew-core はソースビルドが通ることと star 75 以上（自己申請は 225 以上、作成 30 日未満は不可）を要求する。最初は自前の tap になる。Intel macOS は Homebrew の Tier 3 で bottle が付かない点にも注意する。

## 決めるべきこと（未決）

1. **SWE-bench の系統。** `princeton-nlp/*` と `SWE-bench/*` でスキーマが違う（後者は FAIL_TO_PASS が list 型、`eval_type` 等が追加）。v0 は前者を読んでいる。
2. **正解ファイルの定義。** LocAgent は差分の a 側、Agentless は b 側を使う。リネーム・新規・削除で数字がずれる。v0 は両方を出して `--gold-side` で切り替えられるようにしてあるが、既存研究と並べるときにどちらへ揃えるかは決めていない。
3. **索引の置き場所。** Actions で使うなら `actions/cache`（リポジトリあたり 10 GB）に収まるかどうかで形が決まる。中規模から大規模のリポジトリで索引の大きさを測ってから。

## v0 の実測（2026-10-02）

`princeton-nlp/SWE-bench_Verified` の先頭 5 件、gold-side = a。

| k | file recall@k | acc@k |
|---|---|---|
| 1 | 0.200 | 0.200 |
| 5 | 0.200 | 0.200 |
| 10 | 0.400 | 0.400 |

検索の応答時間の中央値 23 ms（索引済みの状態）。索引の規模は astropy で約 1,210 ファイル。

外れた 3 件はいずれも原因が同じで、`.github/ISSUE_TEMPLATE/*.md` が上位を占めた。BM25 の文書長正規化が短い定型 Markdown を優遇し、「issue」「bug」「expected」といった一般語で高く出る。停止語を足してなお残る。次に効く手は 3 つあり、どれもモデルではなく検索側の修正である。

- 許可拡張子から `.md` を外すか、重みを下げる
- `.github/` を索引から除く
- `bm25(files, 2.0, 1.0)` のようにパス列を重く見る

**この 5 件は全て astropy なので、分布として代表していない。** 次は件数を増やし、リポジトリを散らしてから基準線を確定する。
