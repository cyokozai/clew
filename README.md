# clew

> **clew** (n.) — the ball of thread that leads out of the labyrinth. The word *clue* comes from it.

Give clew a GitHub issue. It gives you back the places in the code that the issue is about — with the dependencies that connect them, as line-anchored URLs.

```console
$ clew locate kubernetes/kubernetes 118261

  0.94  direct         staging/src/k8s.io/component-helpers/resource/helpers.go#L120-L168
  0.71  caller         pkg/api/v1/resource/helpers.go#L88-L141
  0.63  same setting   pkg/kubelet/cm/cpumanager/policy_static.go#L402-L430
```

Not one file. The set — because the code that causes the behaviour and the code that breaks when you change it are rarely the same lines.

## How it works

1. **Index** — tree-sitter extracts symbols and three kinds of edge: calls, imports, and shared identifiers (config keys, env vars, error strings). Pinned to a commit SHA, cached in SQLite.
2. **Retrieve** — three queries are built from the issue: prose, stack-trace frames, and quoted error strings. BM25 and embeddings, union of the top 50–100.
3. **Rerank** — a code-specialised cross-encoder scores each candidate against the issue.
4. **Judge** — a *System One* model answers typed questions with calibrated probabilities: does this location relate to the reported behaviour, would changing it change the behaviour, does it match the reported configuration. Several questions ride in one request.
5. **Expand and select** — walk one or two hops along the dependency graph, then pick the smallest set that covers the issue, discounting candidates that repeat what is already covered.

No autoregressive generation anywhere in the loop. A query against an indexed repository answers in seconds, for well under a cent.

## Swappable backends

Both model layers are pluggable, and clew ships with permissively licensed defaults.

| Layer | Default | Also supported |
|---|---|---|
| Reranker | Qwen3-Reranker (Apache-2.0, runs locally) | SweRank (cc-by-nc-4.0, bring your own), any cross-encoder |
| Judge | Laya or Kev (Apache-2.0, run locally) | Jev / TypeSafe, or anything else speaking `POST /v1/systemone` |

`/v1/systemone` has become the common interface for typed-probability models, so the judge is a configuration line, not a dependency. Calibration is kept on our side (isotonic regression against a held-out set) rather than trusted from the vendor — published third-party measurements put raw expected calibration error around 0.105, and around 0.017 after recalibration.

## Status

Design stage. Nothing runs yet. The design is in [docs/design.md](docs/design.md) (Japanese; translation welcome).

## Evaluation

Measured on SWE-bench and SWE-bench Verified, where each issue comes with the patch that fixed it:

- file recall @k — does the top-k contain a file the patch touched
- line recall @k — do the returned ranges overlap the changed lines
- set precision — how much of what we return is noise
- latency and cost per query

Baselines that must be reported alongside: BM25 alone, embeddings alone, and the same candidates ranked by a generative model. If retrieval alone solves an issue, clew should say so rather than take credit for it.

## Prior work

This is a crowded field and clew does not pretend otherwise. [Agentless](https://github.com/OpenAutoCoder/Agentless), [LocAgent](https://github.com/gersteinlab/LocAgent), [CoSIL](https://github.com/ZhonghaoJiang/CoSIL), [OrcaLoca](https://github.com/fishmingyu/OrcaLoca) and [SweRank](https://arxiv.org/abs/2505.07849) all locate code from issues, and SweRank reports 88.69% on SWE-bench-Lite. Two things are left on the table, and they are what clew is for: treating localisation as **set selection over a dependency graph** rather than a ranked list, and making the judgement layer a **calibrated, swappable component** instead of a prompt inside an agent.

## License

Apache-2.0
