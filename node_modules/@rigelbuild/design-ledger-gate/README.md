# @rigelbuild/design-ledger-gate

A configurable gate for decision files and their design-record corpus.

## Configuration

Pass a JSON file with these fields:

```json
{
  "designsRoot": "docs/designs",
  "surfaceDepth": 0,
  "governedRoots": ["ui", "server"],
  "historicalChain": [],
  "exemptBranchPrefixes": ["renovate/", "trunk-merge/"],
  "surfaces": ["alpha"],
  "citationAmbiguousPaths": ["shared/README.md"],
  "legs": {
    "citations": false,
    "errata": false,
    "recordLinks": false,
    "mainIds": false
  },
  "counter": {
    "url": "https://example.invalid/counter",
    "partition": "public-docs"
  },
  "remediationDoc": "docs/design-ledger-policy.md"
}
```

`surfaceDepth` 0 stores records under `designsRoot/<area>/` and decisions under `designsRoot/decisions/<area>/`. It requires `governedRoots`. At depth 1, each discovered surface stores decisions under `designsRoot/<surface>/decisions/<area>/` and records elsewhere beneath that surface; a decision may cite a record on another surface. A surface-level `README.md` is not a record. The gate requires decision files only once records exist. `governedRoots` is only valid at depth 0. `historicalChain`, `exemptBranchPrefixes`, `citationAmbiguousPaths`, `legs`, and `remediationDoc` are optional. `citationAmbiguousPaths` defaults to an empty list and names repository-root paths that also exist in sibling repositories. When a citation needs a root-relative retry for one of those paths, the gate refuses it and counts it as repo-ambiguous. When `historicalChain` is absent, any record may be Historical. When present, only listed paths may be Historical; an empty list allows none. `recordStatusScope` is `all` (default) or `changed`. The latter checks no records without PR context and reports `record Status skipped (no PR context)`. All extra legs default to `false`.
`surfaces` optionally lists the counter surfaces this repository may claim. At depth 0 it must contain exactly one directory name; at depth 1 the gate reports discovered surfaces that are not listed. Use this list when the repository claims decision IDs from the counter.
Record `Status:` headers are case-insensitive. The key may be bold, and spaces may appear before its colon. `Historical` and `Superseded by <path>` values may be bold. A supersession value may include a free-text reason after the path.

When `errata` is enabled, an `## Errata` section must be the final H2. Each entry starts with `### E<n> — YYYY-MM-DD (<who>)`, with IDs in order from E1. The first quoted text after each entry heading must appear verbatim above the section. The first header-zone `Errata: E1, E2` marker must list the same IDs in order. The marker and section require each other.

Citation references use backtick-wrapped `path.md:N` or `path.md:N-M` on a Markdown line. Paths resolve relative to the citing file first. Only directory-qualified targets retry from the repository root; a bare filename never falls back to the root. Missing targets are counted as unresolvable, and configured root targets are counted as repo-ambiguous. The success summary reports checked and seen citations and both counts. Record links must use a live heading anchor when the target exists. The `mainIds` check reads the fetched base ref tree and rejects new decision IDs that already occur on that base tip.

## CLI

```sh
design-ledger-gate --config ./design-ledger-gate.json
```

`GATE_ROOT` optionally sets the repository root. Otherwise the current Git root is used. In pull-request builds, GitHub PR metadata or the Woodpecker pull-request variables provide changed files, body, and branch. Without PR context, PR-only checks are skipped.

## Exit codes

- `0`: no violations.
- `1`: one or more violations.
- `2`: usage, configuration, or read error, including an empty `designsRoot` tree.
