# TODO

Future features for v2, ordered by impact.

## 1. `gitcortex serve` — web dashboard

Local HTTP server rendering stats as interactive visualizations.

- Activity timeline chart
- Coupling as a force-directed graph
- Working patterns as a color heatmap
- Contributors as a ranked bar chart
- Churn risk as a treemap by directory

Transforms the CLI from a personal tool into a team tool. Could use embedded static assets (no external dependencies) with a lightweight charting library.

## 2. Multi-repo aggregation

Aggregate stats across multiple repositories into a single report.

```bash
gitcortex extract --repo ./svc-auth --output data/auth.jsonl
gitcortex extract --repo ./svc-payments --output data/payments.jsonl
gitcortex extract --repo ./svc-gateway --output data/gateway.jsonl

gitcortex stats --input data/*.jsonl
```

Essential for microservice architectures where no single repo tells the full story. Requires prefixing file paths with repo name to avoid collisions. Contributor and coupling analysis across repo boundaries would reveal cross-team dependencies.

## 3. CI integration

Output format compatible with CI systems for automated quality gates.

```bash
gitcortex ci --input data.jsonl \
  --fail-on-busfactor 1 \
  --fail-on-churn-risk 500 \
  --format github-actions
```

Emit GitHub Actions annotations, GitLab code quality reports, or generic SARIF. Flag files with bus factor 1 and high churn risk as warnings in pull requests. Could also track metrics over time and fail on regressions.

## 4. `.mailmap` support

Respect git's `.mailmap` file to unify developer identities.

Currently, the same developer with different emails (e.g., `alice@work.com` and `alice@personal.com`) appears as separate contributors, splitting their stats across identities.

Git's `.mailmap` format already solves this:

```
Alice Developer <alice@work.com> <alice@personal.com>
```

Implementation: parse `.mailmap` during extraction, normalize author/committer emails before emitting records. Affects all downstream stats (contributors, ranking, bus factor, coupling, dev network).
