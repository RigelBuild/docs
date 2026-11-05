# meissa: TypeScript to Go migration

Linear: RIG-5901. Matt's ruling B (2026-10-19): every non-frontend TypeScript
surface ports to Go now, not on touch. Ruling of 2026-11-01: Compass is archived
and tabit is deleted, so neither is a consumer this record waits on. Matt ruled
all eight open questions as recommended on 2026-11-02 (§ Resolved decisions).

Ledger-impact: [DL-647](decisions/go/DL-647.md) records the module contract:
module path, tag scheme, the two public packages and the `tools` binaries.

<!-- LENGTH §Plan: about 5,500 prose words; the Plan holds most of them as per-task Interfaces, parity rules and quoted TS evidence that cheap executors read instead of re-reading the TypeScript sources -->

## Resolved decisions

Matt ruled every question as recommended on the RIG-5901 decision issue
(2026-11-02). Citations name a symbol and a repository-relative path; meissa
paths are at main `2b92002a544e`.

| Question | Decision | Where |
| --- | --- | --- |
| 1 TSX and JSX parser for `commentscan` | **A.** `github.com/odvcencio/gotreesitter`, standalone `grammars/tsx` only. A tree where `(*Node).HasErrorOrMissing` is true sets `Ambiguous`, so a broken file fails closed as `scanJsx` does today. | M1b, M2 |
| 2 JavaScript regex engine for `ref-gate` | **A.** `github.com/dlclark/regexp2/v2` with `ECMAScript` (plus `Unicode` for `u`). Consumer configs use nested lookaheads RE2 cannot compile. A 2-second `MatchTimeout` and the backtracking stack limit end in exit 2 with a named message. Internal patterns use stdlib `regexp`. | M3 |
| 3 Go toolchain and `go` directive | **A.** Build with `go_1_27` (`buildGo127Module`, 1.27.3 in the pinned nixpkgs), directive `go 1.26.0`, so importers on 1.26 and 1.27 both work. | M1a |
| 4 A `comment-scan` binary | **A.** Library only. `packages/comment-scan/package.json` has no `bin`; `tools` ships no `comment-scan` binary. | M2, M3 |
| 5 `renovate-preflight` | **A.** Delete without a port. Nothing in scope runs it. | M12, M16 |
| 6 npm deprecation timing and auth | **A.** Deprecate each retired package once its M16 rows are done. Nemo runs one owner action with a short-lived granular token; trusted publishing cannot run `npm deprecate`. No standing secret. | M16 |
| 7 RigelBuild/docs scope | **A.** M15 swaps the three CLIs to `tools` and ports `site/scripts/gather.ts` (onto `ledger`), its test and `site/scripts/ref-gate-config.test.ts`. `deploy.ts` stays TS; its only import is `bun`. | M15 |
| 8 Markdown parser for `length-gate` | **A.** `github.com/yuin/goldmark` with GFM, `extension.Footnote` (GFM in goldmark omits footnotes) and `go.abhg.dev/goldmark/frontmatter` limited to YAML. A golden mismatch blocks M10 and is fixed in the word walker, never by changing the golden. | M10 |

## Problem / Intent

meissa ships eight TypeScript tools and two JSON config packages to npm. The
tools run under Bun in every consumer's CI. RIG-5901 moves every non-frontend
TypeScript surface to Go. meissa must ship the same tools as one Go module,
`github.com/RigelBuild/meissa/go`, with static binaries in its flake. The CLI
contracts (flags, env, messages and exit codes) must not change. The npm tool
packages then retire.

## Approach

- **One module in `go/`.** Module path `github.com/RigelBuild/meissa/go`, tags
  `go/vX.Y.Z`. Mains live in `go/cmd/<tool>`. Two packages are public:
  `go/commentscan` and `go/ledger`. Every other package is under
  `go/internal/<pkg>`, so it is not importable API. A public module path makes
  every non-internal package importable, and importers then depend on all of
  it.
- **Directory-aligned layout.** Each Go package directory maps to one TS source
  directory, as the table below shows. A future single-repository merge derives
  parent and child components from directory nesting, so `go/` is one
  component and each package directory can become a child without a move.
- **Binaries in the flake.** `go/package.nix` builds every `go/cmd/*` binary
  with `buildGo127Module` and `CGO_ENABLED=0`. The flake exports it as
  `packages.<system>.tools`. Renovate refreshes its `vendorHash` through the
  existing hash-refresh tool, which ports to Go in M4.
- **Same contract, new runtime.** Each binary keeps the argv, env names, output
  lines and exit codes of its parity baseline (see Consumer contract). A
  consumer swaps `bunx <tool>` or `bun run` for the flake binary with the same
  flags and env.
- **Release.** In M2, release-please gains a `go` package with
  `release-type: go` and tag separator `/`, so its tags are `go/vX.Y.Z`. The
  first tag therefore carries the complete `commentscan` API. The npm packages
  keep their `node` release until each one retires.
- **Parity by transcription.** Every TS test becomes a same-named Go subtest.
  Where output is a contract (gate messages, rendered indexes, Markdown
  fingerprints, prose word counts), the Go tests compare against committed TS
  goldens.
- **Clean cutover per port.** Each port PR deletes its `packages/<pkg>` tree,
  its `moon.yml`, its release-please entries and its row in
  `package-bins.test.ts`. Published npm versions stay installable, so
  consumers move on their own schedule.

| TS source | Go package | Shipped as |
| --- | --- | --- |
| `packages/comment-scan` | `go/commentscan` | public library |
| `packages/design-ledger-gate/decision-files.ts` | `go/ledger` | public library |
| `packages/design-ledger-gate/index.ts` | `go/internal/ledgergate` | `design-ledger-gate` |
| `packages/dl-claim` | `go/internal/dlclaim` | `dl-claim` |
| `packages/dl-list` | `go/internal/dllist` | `dl-list` |
| `packages/dl-reconcile` | `go/internal/dlreconcile` | `dl-reconcile` |
| `packages/length-gate` | `go/internal/lengthgate` | `length-gate` |
| `packages/ref-gate` | `go/internal/refgate` | `ref-gate` |
| `tools/renovate` | `go/internal/renovatehashes` | `refresh-nix-hashes` (not in `tools`) |
| `tools/release` | `go/internal/npmpublish` | `npm-publish` (not in `tools`) |
| root `*.test.ts` | `go/internal/repochecks` | tests only |
| none (shared JS semantics) | `go/internal/jstext` | internal helper |
| `diff` npm package (jsdiff) | `go/internal/linediff` | internal helper |
| `packages/renovate-preflight` | none | deleted (decision 5) |

### Inventory

Measured with `wc -l` at meissa main `2b92002a544e`. Totals: 17,792 lines,
8,320 non-test and 9,472 test.

| Surface | Non-test files / LOC | Test files / LOC | Test cases | npm |
| --- | --- | --- | --- | --- |
| `packages/comment-scan` | 8 / 2,978 | 12 / 1,869 | 176 | yes, library |
| `packages/design-ledger-gate` | 2 / 1,985 | 2 / 2,796 | 102 | yes, bin + library |
| `packages/dl-claim` | 1 / 519 | 1 / 522 | 21 | yes, bin |
| `packages/dl-list` | 1 / 257 | 1 / 346 | 18 | yes, bin |
| `packages/dl-reconcile` | 1 / 355 | 1 / 390 | 15 | yes, bin |
| `packages/length-gate` | 5 / 1,262 | 2 / 1,584 | 87 | yes, bin |
| `packages/ref-gate` | 1 / 355 | 1 / 407 | 22 | yes, bin |
| `packages/renovate-preflight` | 2 / 230 | 2 / 338 | 14 | yes, bin |
| `tools/release` | 1 / 84 | 1 / 102 | 6 | no |
| `tools/renovate` | 1 / 295 | 2 / 669 | 17 | no |
| root tests | 0 | 4 / 449 | 14 | no |

Purpose, per surface:

- **comment-scan:** language-aware comment residue scanning for 15 language
  ids, unified-diff parsing, comment blocks, cgo detection and Markdown
  structure fingerprints. `jsx.ts` uses the `typescript` compiler; `astro.ts`
  splits Astro files into script, style and template segments.
- **design-ledger-gate:** `decision-files.ts` parses one decision file and
  renders a decision index. `index.ts` is the gate: config, path
  classification, record `Status:` headers, supersession, touch coupling,
  citations, errata, record links, main ids and the claim check.
- **dl-claim, dl-list, dl-reconcile:** claim DL ids from the counter service,
  list decision files with filters, and reconcile landed ids with the service.
- **length-gate:** comment-block ceiling ratchet and per-class document word
  ratchet over a git diff, with `LENGTH` markers.
- **ref-gate:** a `git grep -E` prefilter, then JavaScript regex patterns with
  an allowlist and carve-outs.
- **renovate-preflight:** classifies a `gh api graphql` probe of the Renovate
  token.
- **tools/release:** `publish.ts` publishes the npm packages that release-please
  released and skips versions already on npm.
- **tools/renovate:** `refresh-go-analysis-hashes.ts` refreshes source and
  vendor hashes in `tools/toolchain/go-analysis-pins.nix`; `config.test.ts`
  checks the Renovate config.
- **root tests:** `package-bins.test.ts` (npm bins), `ref-gate.config.test.ts`
  (meissa's own ref-gate config), `rumdl-extends.test.ts` (the shared rumdl
  policy) and `shared-configs.test.ts` (the two JSON config packages).

One `comment-scan` change on main is not on npm: "fix(comment-scan): scan
template text after a markup expression (RIG-5717)" changes `astro.ts` after
the `comment-scan@v1.0.0` tag. M2 handles it.

### What stays TypeScript

`packages/biome-config` (1.0.1) and `packages/tsconfig` (1.0.0) stay npm
packages. They hold JSON only, with no TS code and no `moon.yml`. Biome and
`tsc` resolve them through Node package exports, so a Go module cannot carry
them. They serve frontend repositories, which stay TypeScript under RIG-5901.
They keep `release-type: node` and the npm publish step. Their test,
`shared-configs.test.ts`, ports to Go in M13 and runs `node` and `biome` as
child processes.

## Alternatives considered

- **Go beside the TS in `packages/<pkg>`.** This keeps the old directories, but
  it puts the module root at `packages/` or forces one module per package. It
  breaks the agreed module path and multiplies tags.
- **One module at the repository root.** This needs path
  `github.com/RigelBuild/meissa`, which the consumers do not expect, and it
  makes root files part of the module.
- **Publish Go binaries as GitHub release assets.** Consumers already take
  rumdl and biome from the meissa flake. A flake package keeps one install path
  and exact derivations.

## Plan

### Global Constraints

- **Module:** `github.com/RigelBuild/meissa/go` in `go/`. Directive `go 1.26.0`;
  build toolchain `go_1_27` (decision 3).
- **Build env:** `GOTOOLCHAIN=local`, `GOWORK=off`, `GOFLAGS=-mod=readonly`.
  Release binaries use `CGO_ENABLED=0`. Tests run `CGO_ENABLED=1 go test -race
  ./...`.
- **Layout:** mains only in `go/cmd/<tool>`, each under 40 lines that call
  `internal/<pkg>.Main(ctx, os.Args[1:], env, stdout, stderr) int`. Public
  packages are only `go/commentscan` and `go/ledger`.
- **Libraries:** stdlib first. `log/slog` for logs that are not contract
  output. `golang.org/x/sync/errgroup` for concurrency. `github.com/samber/oops`
  for wrapped errors with `(T, error)` returns. `github.com/samber/mo` for
  `Option[T]` only, on nullable struct fields. `github.com/samber/lo` only where
  it beats stdlib. Add a library with its first call site.
- **Pinned third-party choices:** `github.com/bmatcuk/doublestar/v4` for globs;
  `github.com/dlclark/regexp2/v2` for user-authored JS regexes;
  `github.com/yuin/goldmark` plus `go.abhg.dev/goldmark/frontmatter` for
  Markdown; `github.com/odvcencio/gotreesitter/grammars/tsx` for TSX, and no
  other gotreesitter grammar package; `go.yaml.in/yaml/v3` for YAML. Never
  import the archived `gopkg.in/yaml.v3` directly.
- **Glob semantics:** `Bun.Glob.match` matches dot paths, so pattern checks use
  `doublestar.Match`. `Bun.Glob.scan` skips dot paths, so directory scans use
  `doublestar.GlobWalk` with `doublestar.WithFilesOnly()` and
  `doublestar.WithNoHidden()`.
- **Regex:** stdlib `regexp` for internal patterns. Rewrite the six patterns
  RE2 rejects (a backreference or lookahead) as small hand matchers with a test
  per rewrite: `ONE_LINE_ISLAND` in `astro.ts`, the `url(` lookahead in
  `languages.ts`, the backslash-before-pipe pattern in `decision-files.ts`, and
  the three script and style tag patterns in `length-gate/core.ts`.
- **JS text semantics (`go/internal/jstext`):** RE2 `\s`, `strings.TrimSpace`
  and `strings.ToLower` differ from JavaScript. Every port uses this helper
  wherever the TS splits, trims, lowercases or matches `\s`:
  - `IsSpace(r rune) bool`: the ECMAScript WhiteSpace and LineTerminator set,
    which adds U+00A0, U+FEFF, U+2028, U+2029 and the Zs category to ASCII
    space.
  - `Fields(s string) []string` for `split(/\s+/)`, and `Trim(s string) string`
    for `trim()`.
  - `ToLower(s string) string`: like `toLowerCase()`, it maps a word-final
    U+03A3 to U+03C2 and U+0130 to `i` plus U+0307.
  - A TS replacement such as `replace(/\s+/g, " ")` becomes a hand loop over
    `IsSpace`, not a `regexp` with `\s`.
  - Sticky (`/y`) patterns and `lastIndex = i` loops become an anchored match
    on `text[i:]`: a `^`-prefixed stdlib `regexp` applied to the suffix.
- **Type mapping:** each exported TS type becomes a Go struct with the same
  fields in Go case. A nullable field (`T | null`) becomes `mo.Option[T]`,
  except where an empty value is invalid by the TS contract; then it is the
  zero value and the doc comment says so. An optional `RegExp` field or a
  recursive config field becomes a pointer, `nil` for absent (M1b). A nullable
  return becomes `(T, bool)`. A tagged union becomes a sealed interface with
  one struct per arm. A TS `throw` becomes a returned `error`. Every type a
  task names is spelled out in its Interfaces block; where a block says "the
  executor reads the TS", it names the exact TS symbol.
- **CLI contract:** same flags, env names, stdout and stderr lines and exit
  codes as the parity baseline. Exit 0 is pass, 1 is findings, 2 is usage,
  config or I/O error, unless the TS tool says otherwise.
- **Analysis:** golangci-lint v2 with `default: all` and a reasoned disable list
  in `go/.golangci.yml`; govulncheck; go-licenses; nilaway as an advisory lane.
  All four come from meissa's own `lib.goAnalysis` built with `go_1_27`.
- **Parity:** every TS `test` becomes a Go subtest with the same name. The PR
  body states the TS and Go case counts and names every dropped case with its
  reason. Write the Go tests first against a compiling stub and watch them
  fail.
- **TS goldens:** the capture script `tools/golden/capture.ts` (added in M2,
  deleted in M14 when Bun leaves) checks out a named meissa revision in a
  temporary `git worktree`, runs the TS function over an input directory, and
  writes one JSON golden per input. Each golden file records the revision.
  - The capture revision is the parent of the port PR's deletion commit, so it
    includes every unreleased TS fix.
  - Inputs are committed under `go/<pkg>/testdata/corpus/`, goldens under
    `go/<pkg>/testdata/ts-golden/`, at most 1.5 MiB per package.
  - A docs-repository input set is every tenth `.md` file under
    `docs/designs/` in sorted path order, at a docs commit the PR names, until
    the cap. RigelBuild/docs is public, so copies are allowed.
  - A run over a full external corpus is one-shot evidence: the PR body gives
    the command and the counts only.
  - Two messages are prefix goldens, because the engine text after the prefix
    differs: `malformed YAML:` and `Invalid regular expression in patterns[N]:`.
- **Licenses:** a ported algorithm keeps its upstream notice in the file
  header. The line diff in `go/internal/linediff` is a port of jsdiff
  (BSD-3-Clause, Kevin Decker).
- **Public prose:** this repository and RigelBuild/docs are public. Records
  and PR bodies call any other repository "a consuming repository" and never
  quote its code.
- **Markdown:** every changed `.md` passes `rumdl check`.

### M1a: Go module, repository plumbing, `diff.go`

Create the module and port `diff.ts` minus its scanner routing, with
`diff.test.ts`. `ScanText` and `ResidueScannerFor` arrive in M1b.

Plumbing in the same PR:

- `go/go.mod`, `go/.golangci.yml`, `go/moon.yml` (`layer: 'tool'`,
  `language: 'go'`, `tags: ['go']`).
- `.moon/tasks/tag-go.yml` with tasks `build` (`go build ./...`), `test`
  (`go test -race ./...` with `CGO_ENABLED=1`), `lint` (`golangci-lint run
  ./...`), `vuln` (`govulncheck ./...`), `licenses` (`go-licenses check
  ./...`), `nilaway` (`nilaway ./...`, advisory, `allowFailure: true`) and `ci`
  depending on all of them. Each task sets the Global Constraints build env.
- `.moon/workspace.yml` globs gain `'go/moon.yml'`.
- `flake.nix` gains `packages.<system>.go-dev`: a `symlinkJoin` of
  `pkgs.go_1_27` and `self.lib.goAnalysis { inherit pkgs; goToolchain =
  pkgs.go_1_27; }`. `.github/workflows/ci.yml` puts `go-dev` on `PATH` the way
  it puts `bun` there today.
- `devenv.nix` imports `flake.devenvModules.go`, sets `meissa.go.toolchain =
  pkgs.go_1_27`, and adds `pkgs.go_1_27`. `devenvModules.go` keeps its option
  and output unchanged.
- No release-please change. Nothing under `go/` is tagged before M2.

Interfaces (package `commentscan`):

```go
type DiffLine struct {
    Kind  byte // ' ', '+' or '-'
    Text  string
    OldNo int
    NewNo int
}

type Hunk struct{ Lines []DiffLine }

type FileDiff struct {
    Path, OldPath, NewPath string
    Hunks                  []Hunk
    Added, Deleted         bool // the file is new / the file is removed
    Binary, MetadataOnly   bool
}

func ExtOf(path string) string // "" when the last dot is at index <= 0, else the lowercased extension
func ParseDiff(diff string) []FileDiff
```

### M1b: `commentscan` scanners part 1

Port `scan.ts`, `languages.ts`, `comment-blocks.ts` and the scanner routing in
`diff.ts`, using `jstext` for the sticky patterns (`GO_IDENTIFIER`, the heredoc
pattern and every `openRe`). `.tsx`, `.jsx` and `.astro` stay unrouted until
M2: `ResidueScannerFor` returns `false` for them, and the M2 tests cover them.
Port `scan.test.ts`, `config-languages.test.ts`, `css.test.ts`,
`kdl.test.ts`, `malformed.test.ts`, `shell-sql.test.ts` and
`comment-blocks.test.ts`. Delete them from the TS package in the same PR. The
TS package stays for M2.

Interfaces (package `commentscan`):

```go
type LanguageID string // "go" "ts" "rust" "nix" "python" "toml" "yaml" "shell" "sql" "kdl" "css" "html" "tsx" "astro" "proto"

type Residues struct {
    Residues  []string // one entry per source line
    Ambiguous bool
}

type CommentBlock struct {
    Start int    // 1-based first line
    Lines int    // line count
    First string // text of the first line
    Doc   bool
}
type BlockScan struct { Blocks []CommentBlock; Ambiguous bool }

func ScanText(lang LanguageID, text string) (Residues, error) // error: "no scanner for language <id>"
func ResidueScannerFor(ext string) (func(text string) Residues, bool)
func CommentBlocks(text, ext string) (BlockScan, bool)
func IsCgo(text string) bool
```

`packages/comment-scan/index.ts` re-exports the config type
(`export type { LangConfig } from "./scan.ts";`), and `Segment` in `astro.ts`
holds it (`lang: LangConfig;`). So `LangConfig`, the types its fields name
(`StringSpec`, `EscapeMode`) and `ExprSpec` (the inline type of its `expr`
field) are exported Go types under the Type mapping rule. The fields follow
`export type LangConfig` and `export type StringSpec` in `scan.ts`, in the
same order:

```go
type EscapeMode string // "backslash" "nix-indented" "doubled" "none"

type StringSpec struct {
    Open, Close   string
    Escape        EscapeMode
    Multiline     bool
    Interpolation bool
    InterpOpen    string      // "" means the default "${"
    InterpClose   string      // "" means the default "}"
    InterpLang    *LangConfig // nil keeps the enclosing language
    CloseIsOpen   bool
    OpenRe        *regexp.Regexp // "^"-anchored; matched on text[i:] (TS sticky /y)
    OpenAfter     *regexp.Regexp // tested against the right-trimmed residue
}

type ExprSpec struct {
    Open, Close string
    Lang        *LangConfig
}

type LangConfig struct {
    ID               LanguageID
    LineComment      []string
    LineCommentAfter *regexp.Regexp // nil means a line comment may start anywhere
    BlockComment     bool
    BlockOpen        string // "" means the default "/*"
    BlockClose       string // "" means the default "*/"
    NestBlock        bool
    RustChar         bool
    RegexLiteral     bool
    Strings          []StringSpec
    Expr             *ExprSpec      // nil means none
    IndentScalar     *regexp.Regexp // nil means none
    Heredoc          *regexp.Regexp // "^"-anchored; nil means none
    Directive        *regexp.Regexp
}
```

Optional regex and recursive fields are pointers, not `mo.Option`, because
`nil` is the natural "absent" for these types and the scanner never copies
them. A `""` default is valid only where the TS falls back to a default
token. `ScanState`, `scanLines` and `scanComplete` stay unexported; the
executor reads those TS symbols directly.

### M2: `commentscan` part 2, public API complete and tagged

Port `markdown.ts`, `jsx.ts`, `astro.ts` and the `index.ts` export list. Route
`.tsx`, `.jsx` and `.astro` in `ResidueScannerFor`.

Unreleased TS fix first: the pending release-please PR ships
`@rigelbuild/comment-scan` 1.0.1 with the RIG-5717 Astro fix before M2 merges.
M2 then pins `packages/length-gate/package.json` to `@rigelbuild/comment-scan`
`1.0.1`. Once the workspace package is gone, the TS `length-gate` resolves that
version from npm and keeps the fix until M11 deletes it. The `node-workspace`
plugin releases that bump as `length-gate` 1.0.2.

Parity for TSX and Astro (meissa and RigelBuild/docs hold no `.tsx`, `.jsx` or
`.astro` file):

- **One-shot evidence:** run the TS-vs-Go `scanJsx` and `scanAstro` comparison
  once over the `.tsx` and `.astro` files of the consuming repositories in
  RIG-5901 scope. The PR body gives counts only. Mismatch excerpts go on the
  RIG-5901 issue, never in the public PR.
- **Committed corpus:** the TSX cases of tree-sitter-typescript's `test/corpus`
  (MIT) extracted to `.tsx` files, plus the `jsx.test.ts` and `astro.test.ts`
  edge cases expanded to whole files, with TS goldens.
- **Broken input, both directions:** cases where the TS parser reports a
  diagnostic and tree-sitter recovers, and the reverse. Each one must end
  `Ambiguous`. Start from the `jsx.test.ts` case "a parse error is ambiguous".

Markdown goldens: `markdownFingerprint` and `compareMarkdown` over the meissa
`.md` files and the docs input set (Global Constraints, TS goldens).

`repochecks` starts here (`go/internal/repochecks`, test-only):

- **Grammar ban:** `go list -deps ./...` must not list
  `github.com/odvcencio/gotreesitter/grammars`. That aggregate package embeds
  GPL-3.0 grammar tables, and go-licenses cannot see them because the module
  license is MIT.
- **Importer smoke test** (replaces `consumer-install.test.ts`): a temporary
  module requires `github.com/RigelBuild/meissa/go` with a `replace` to the
  local `go/`, imports `commentscan`, and runs `go build` with
  `CGO_ENABLED=0` and no build tags.

Release plumbing moves here from M1, so the first tag is the complete API:

- release-please: add package `go` with `"release-type": "go"`,
  `"component": "go"`, `"tag-separator": "/"`, `"initial-version": "0.1.0"`.
  No manifest entry until the first release writes one.
- `tools/release/publish.ts` validates `paths_released` as today, then keeps
  only paths under `packages/`. If none remain, it logs
  `skip: no npm package released` and exits 0. Add its tests.

Delete `packages/comment-scan`, its release-please entries and
`consumer-install.test.ts`. When the first `go/v*` release exists (expected
`go/v0.1.0`), record the exact tag on RIG-5901. That tag is the "M2" a
consumer waits on.

Interfaces (package `commentscan`):

```go
type MarkdownItem struct { Kind string; Value string } // Kind: the 14 TS MarkdownItemKind strings
type MarkdownScan struct {
    Items  []MarkdownItem
    Defect string // "" means no defect; else one of the three TS MarkdownDefect strings
}

type MarkdownVerdict interface{ isMarkdownVerdict() }
type VerdictSame struct{}
type VerdictMismatch struct {
    Index         int
    Before, After mo.Option[MarkdownItem]
}
type VerdictMalformed struct{ Side, Reason string } // Side: "before" or "after"

type Segment struct {
    Lang      *LangConfig // the shared table entry; compare by pointer, read Lang.ID
    StartLine int
    Lines     []string
}

func ScanMarkdown(text string) MarkdownScan
func MarkdownFingerprint(text string) []MarkdownItem
func CompareMarkdown(before, after string) MarkdownVerdict
func ScanJSX(text string) Residues
func ScanAstro(text string) Residues
func SegmentAstro(text string) ([]Segment, bool)
```

`ScanJSX` copies `scanJsx` in `packages/comment-scan/jsx.ts` step by step:

- Normalise CRLF first: "`text = text.replace(/\r\n/g, "\n");`".
- A broken parse fails closed: "`return { residues: text.split("\n"),
  ambiguous: true };`". In Go, that is the case when
  `RootNode().HasErrorOrMissing()` is true.
- Directive comments stay in the residue: "`return
  !TS_DIRECTIVE.test(text.slice(range.pos, range.end));`".
- Comment ranges are sorted by start, and a nested range is skipped: "`if
  (range.pos < at) continue;`".
- A removed comment keeps its newlines, so line numbers do not move.
- Each residue line is right-trimmed of spaces and tabs only:
  "`out.split("\n").map((line) => line.replace(/[ \t]+$/, ""))`".

It imports `github.com/odvcencio/gotreesitter/grammars/tsx` and needs no build
tags. `grammars/tsx/tsx.go` carries
"`//go:build !grammar_subset || grammar_subset_tsx`", so an importer that sets
`grammar_subset` must also set `grammar_subset_tsx`.

### M3: flake `tools` package and `ref-gate`

Port `packages/ref-gate` to `go/internal/refgate` and `go/cmd/ref-gate`. Add
`go/package.nix` and export it as `packages.<system>.tools`. Switch the root
`refgate` moon task to `ref-gate --config ref-gate.config.json` from `tools`.

`go/package.nix`:

```nix
{ buildGo127Module, lib }:
buildGo127Module {
  pname = "meissa-tools";
  version = (lib.importJSON ../.release-please-manifest.json).go or "0.0.0";
  src = lib.fileset.toSource { root = ./.; fileset = ./.; };
  vendorHash = "sha256-…";
  subPackages = [ "cmd/ref-gate" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  doCheck = false;
}
```

Write the real hash in the PR; the `…` above is not a literal. Tests run in
moon, not in the derivation. Add `checks.<system>.tools`: a `runCommand` that
runs `test -x` for every name in `subPackages`. Each later binary task appends
its `cmd/<tool>` to `subPackages`. `nix flake check` then catches a stale
`vendorHash`.

Interfaces (package `refgate`):

```go
type Pattern struct{ Source, Flags string }
type Prefilter struct { ERE string; IgnoreCase bool }
type Config struct {
    Prefilter        Prefilter
    Patterns         []Pattern
    Ignore           []string
    CarveOutPaths    []string
    CarveOutPrefixes []string
    Allowlist        map[string]string
    RemediationDoc   mo.Option[string]
}
type Reference struct { File string; Line int; Text string }

func LoadConfig(path string) (Config, error)
func ParseGitGrepOutput(output string) ([]string, error) // "unparsable git grep output at offset N"
func FindViolations(cfg Config, grepHits []string) ([]Reference, error)
func GitGrep(ctx context.Context, root string, p Prefilter) ([]string, error)
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int
```

Flag mapping, checked in `LoadConfig`. The TS hands `flags` straight to
`new RegExp(source, flags)` in `findViolations` and then runs
"`pattern.lastIndex = 0; return pattern.test(matchText);`" for each hit.

- `i` to `IgnoreCase`, `m` to `Multiline`, `s` to `Singleline`, `u` to
  `Unicode`.
- `g` and `d` are accepted and change nothing. The `lastIndex = 0` reset above
  means `g` has no effect in the TS either.
- `y` (sticky) matches only at index 0, because of the same reset: a hit
  counts only when `match.Index == 0`.
- `v` is rejected with the `Invalid regular expression in patterns[N]:` prefix.
  This is a contract change: JavaScript accepts `v`. The README and the M3
  CHANGELOG entry state it.
- Any other flag is rejected the same way.

Ignore names: the TS builds "`new RegExp(escapeRegExp(name), "gi")`" for each
non-empty `ignore` entry and removes every match with
"`matchText = matchText.replace(ignoredName, "")`" before the pattern test.
Go ports `escapeRegExp` as is (it escapes ``.*+?^${}()|[]\``), compiles with
`ECMAScript|IgnoreCase`, and calls `Replace(text, "", -1, -1)`. A hit that
does not parse fails with "`unparsable grep hit: ${JSON.stringify(line)}`".
Go writes the line with a `json.Encoder` that has `SetEscapeHTML(false)`.
U+2028, U+2029 and invalid UTF-8 still differ from `JSON.stringify`, so each
gets a golden.

Match failures: a `MatchTimeout` (2 seconds) or `regexp2.ErrBacktrackingStackLimit`
ends the run with exit 2 and `ref-gate: patterns[N] <timed out | hit the
backtracking limit> on <file>:<line>`. Each failure has its own test. Output
stays `<file>:<line>: <text>`.

### M4: Renovate hash refresh in Go

Port `tools/renovate/refresh-go-analysis-hashes.ts` and its test to
`go/internal/renovatehashes` and `go/cmd/refresh-nix-hashes`. Add a third
entry: the `vendorHash` line in `go/package.nix`, resolved from
`self.packages.x86_64-linux.tools.goModules`. Port `config.test.ts` to
`go/internal/repochecks`, decoding JSON5 with `github.com/titanous/json5`. If
go-licenses rejects it, keep that one test in Bun and say so in the PR.

Renovate changes in `tools/renovate/config.json5`:

- `enabledManagers` gains `gomod`, with `postUpdateOptions: ["gomodTidy"]`.
  The ported config test asserts the new list.
- A packageRule for `github.com/odvcencio/gotreesitter` sets
  `automerge: false`. Its bump PR must pass the M2 TSX corpus test, which
  `go test` runs in CI.
- The global `postUpgradeTasks` command becomes
  `go -C go run ./cmd/refresh-nix-hashes`, with `fileFilters`
  `tools/toolchain/go-analysis-pins.nix` and `go/package.nix`.
- `tools/renovate/bot-config.json5` `allowedCommands` replaces the Bun entry
  with the anchored, escaped regex for that exact command.
- `.github/workflows/renovate.yml` puts `go_1_27` on `PATH`.

Delete the TS tool and its test.

Interfaces (package `renovatehashes`):

```go
type Entry struct {
    Name       string // "nilaway", "golangci-lint", "meissa-tools"
    File       string // file that holds the hash lines
    SourceAttr mo.Option[string]
    VendorAttr string
}

func Refresh(ctx context.Context, root string, entries []Entry, run Runner) error
type Runner interface {
    Run(ctx context.Context, name string, args ...string) (stdout, stderr string, exit int, err error)
}
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int
```

Error text keeps the `renovate-go-analysis:` prefix and the TS messages.

### M5: `ledger` public package

Port `decision-files.ts` to `go/ledger` with `decision-files.test.ts`. Capture
TS goldens for `renderDecisionIndex` over the meissa test fixtures and the
docs decision files (all of them; they are small). `Bun.markdown.html` in the
TS test becomes a goldmark render in the Go test.

Interfaces (package `ledger`):

```go
type DecisionRow struct {
    Path         string // repository-relative decision-file path
    Line         int    // 1-based line of the id key
    ID           string
    Decision     string
    Status       string
    RecordRaw    string
    RecordPath   string
    RecordAnchor string // "" means no anchor; the anchor pattern forbids an empty anchor
}

type MalformedDecision struct {
    Path   string
    Line   int
    Reason string
}
func (e *MalformedDecision) Error() string // "<path>:<line>: <reason>"

func ParseDecisionFile(path string, src []byte) (DecisionRow, error) // error is *MalformedDecision
func RenderDecisionIndex(rows []DecisionRow) string
```

`ParseDecisionFile` keeps the order of checks in `parseDecisionFile` in
`packages/design-ledger-gate/decision-files.ts`, because the first failure is
the reported reason:

1. Strip one leading BOM and split on `\n`: "`text.replace(/^\uFEFF/,
   "").split("\n")`".
2. Line 1, right-trimmed, must be `---`, else line 1, "``file must start with
   a `---` front-matter block``". No later `---` line gives line 1, "``
   front-matter block is not closed with `---` ``".
3. The block holds exactly four lines, `id`, `decision`, `status`, `record`
   (`KEYS`), each matching "`/^([A-Za-z_]+): (.*)$/`" in that order.
4. `decision` and `status` must match `QUOTED_RE` and are decoded with
   `JSON.parse`. Go decodes with `json.Unmarshal` into a `string`, never
   `strconv.Unquote`, which rejects the `\/` escape that `QUOTED_RE` allows.
   A lone `\uD800`-range escape decodes to U+FFFD in Go but stays a lone
   surrogate in JS; it gets a golden. Any other key fails on an empty value
   or a first character in "``/^["'[{|>&*!%@`#]/``".
5. `id` must match `ID_RE` and equal the `FILE_RE` capture of the base name,
   then the `decision`, `status`, `record` and anchor checks follow.
6. Last, the whole block must parse as YAML; the failure is
   "`malformed YAML: ${String(error)}`".

Every TS reason string is a golden. The record value is parsed with
`go.yaml.in/yaml/v3`; its failure reason is the `malformed YAML:` prefix
golden, because the error text after it comes from a different parser.

### M6a: `design-ledger-gate` pure core

Port the pure functions of `packages/design-ledger-gate/index.ts` to
`go/internal/ledgergate`: config loading, path classification, corpus,
`Status:` headers, supersession, touch coupling and `evaluate`. Port the
describe blocks `parseRecordHeader`, `design path discovery`, `touchesRecord`,
`touchesRecord at depth one`, `decision parsing and invariants`,
`record Status headers`, `decision completeness`, `touch coupling`,
`conflictMarkerViolations`, `loadLedgerConfig` and `depth-one layout`.

Interfaces (package `ledgergate`):

```go
type Legs struct { Citations, Errata, RecordLinks, MainIDs, ClaimCheck bool }
type Counter struct{ URL, Partition string }
type ExemptBranchRule struct{ Prefix, Author string }
type Config struct {
    DesignsRoot            string
    SurfaceDepth           int // 0 or 1
    GovernedRoots          []string
    Surfaces               []string
    HistoricalChain        []string
    ExemptBranches         []ExemptBranchRule
    RecordStatusScope      string // "all" (default) or "changed"
    CitationAmbiguousPaths []string
    Legs                   Legs
    Counter                Counter
    RemediationDoc         mo.Option[string]
}

type DesignPathKind string // "decision" "decisions-readme" "legacy-ledger" "misplaced" "other"
type StrayPath struct {
    Path string
    Kind DesignPathKind // "legacy-ledger" or "misplaced"
}
type File struct{ Path, Text string }
type DecisionCorpus struct {
    Rows      []ledger.DecisionRow
    Malformed []ledger.MalformedDecision
    Strays    []StrayPath
}
type RecordHeader struct {
    Path       string
    StatusLine mo.Option[string]
    Line       int
}
type RecordContent struct { Headings []string; SizeBytes int }
type Changed struct {
    Files      []string
    Body       mo.Option[string]
    HeadBranch string
}
type StatusValue interface{ isStatusValue() }
type StatusHistorical struct{}
type StatusSuperseded struct{ Path string }
type PullRequestDetails struct {
    IsCrossRepository   mo.Option[bool]
    HeadRepositoryOwner mo.Option[string]
    Author              mo.Option[string]
}
type Violation struct { File string; Line int; Message string }

func LoadConfig(path string) (Config, error)
func ClassifyDesignPath(file string, cfg Config) DesignPathKind
func TouchesRecord(file string, cfg Config) bool
func Slugify(heading string) string // uses jstext.Trim and jstext.ToLower
func ParseStatusValue(line string) (StatusValue, bool)
func ParseRecordHeader(path, text string) RecordHeader
func RecordContentFromText(text string) RecordContent
func ConflictMarkerViolations(file, text string) []Violation
func BuildDecisionCorpus(files []File, strays []StrayPath) DecisionCorpus
func Evaluate(corpus DecisionCorpus, records []RecordHeader, changed Changed,
    readRecord func(path string) (RecordContent, bool), cfg Config,
    details mo.Option[PullRequestDetails]) []Violation
func IsExemptBranch(headBranch string, author mo.Option[string], crossRepository mo.Option[bool], rules []ExemptBranchRule) bool
```

Every violation message in the TS tests is a golden.

### M6b: `design-ledger-gate` runner and validation legs

Port `runOnce` with its four optional legs (citations, errata, record links,
main ids), `prContextFrom`, `parsePrMetadata` and `readPullRequest`. Port the
describe blocks `runOnce`, `prContextFrom` and `optional validation legs`. The
claim-check leg is wired in M7; until then `RunOnce` treats `Legs.ClaimCheck`
as a config error, and the claim-check cases of `optional validation legs`
move to M7.

Interfaces (package `ledgergate`):

```go
type ClaimVerdict string // "lane" or "not-lane"

type Deps interface {
    Root() string
    ReadText(path string) (string, bool, error)
    ListDesignFiles(ctx context.Context) ([]string, error)
    ReadMergeBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error)
    ReadBaseDecisionPaths(ctx context.Context, baseRef string) ([]string, error)
    Changed(ctx context.Context) (Changed, error)
    FetchClaims(ctx context.Context, lane string, ids []string) (map[string]ClaimVerdict, error)
    PullRequestDetails(ctx context.Context) (mo.Option[PullRequestDetails], error)
}

type PRContext interface{ isPRContext() }
type PRContextPR struct{ Repo, PRNumber string }
type PRContextWoodpecker struct{ Changed Changed }
type PRContextSkip struct{}
type PRContextError struct{ Message string }

type PRMetadata struct {
    Changed            Changed
    PullRequestDetails PullRequestDetails
}

func PRContextFrom(env func(string) string) PRContext
func ParsePRMetadata(raw []byte) (PRMetadata, error)
func ReadPullRequest(ctx context.Context, repo, number string) (PRMetadata, error)
func RunOnce(ctx context.Context, deps Deps, cfg Config, stdout, stderr io.Writer) int
```

`gh` calls keep the TS argv, including the `timeout 30` and `timeout 60`
wrappers. Errata and citation scans use `jstext` wherever the TS uses `\s`.

### M7: `design-ledger-gate` claim check and CLI

Port the claim client, `claimCheckIds`, `checkClaims`, `readDlClaimToken`,
`validateDlClaimToken` and the CLI `main`. Port the describe blocks
`CLI arguments` and `claim check`, plus the claim-check cases moved from M6b.
Add `cmd/design-ledger-gate` to `tools`. Delete `packages/design-ledger-gate`.

Interfaces (package `ledgergate`):

```go
type ClaimServiceError struct {
    Kind    string // "unreachable" or "rate_limited"
    Message string
}
func (e *ClaimServiceError) Error() string

type ClaimFetch func(ctx context.Context, lane string, ids []string) (map[string]ClaimVerdict, error)

func NewClaimClient(cfg Config, httpClient *http.Client, clock Clock) ClaimFetch
type Clock interface { After(d time.Duration) <-chan time.Time }
func ClaimCheckIDs(rows []ledger.DecisionRow, changedFiles []string, mergeBaseIDs, baseTipIDs map[string]struct{}) []ledger.DecisionRow
func CheckClaims(rows []ledger.DecisionRow, lane string, verdicts map[string]ClaimVerdict) []Violation
func ReadDLClaimToken(env func(string) string) (string, error)
func ValidateDLClaimToken(token string) (string, error)
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int
```

The claim client keeps the TS timing: 10-second timeout, 3 attempts, backoff of
1 and 3 seconds, and at most a 65-second wait on a rate limit. Tests inject the
`Clock`; they never sleep.

### M8: `dl-claim`, `dl-list`, `dl-reconcile`

Port the three packages to `go/internal/dlclaim`, `go/internal/dllist`,
`go/internal/dlreconcile` and their `go/cmd/*` mains. They import
`ledgergate.LoadConfig`, `ledgergate.ReadDLClaimToken`,
`ledgergate.ClassifyDesignPath` and `ledger.ParseDecisionFile`. Add the three
binaries to `tools`. Delete the three TS packages.

Interfaces:

```go
// package dlclaim
type Args interface{ isArgs() }
type ClaimArgs struct { Ref, Lane string; Count int; Surface mo.Option[string] }
type RebindArgs struct { From, Lane string; IDs []string }
type ReleaseArgs struct { Lane string; IDs []string }
type ClaimRequest struct { Repo, Surface, Ref, Lane string; Count int }
type ClaimedID struct{ ID, Date string }
func ParseArgs(argv []string, cfg ledgergate.Config) (Args, error)
func BuildClaimBody(cfg ledgergate.Config, a ClaimArgs) ClaimRequest
func FormatClaimed(ids []ClaimedID) string // "<id> (claimed <date>)" per line
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int

// package dllist
type DecisionFile struct{ Path, Text string }
type Filters struct {
    Surface, Area mo.Option[string]
    Status        mo.Option[string] // "active", "superseded" or "retired"
    Grep          mo.Option[string]
}
type ParsedArgs struct {
    Filters    Filters
    ConfigPath string // default "design-ledger.config.json"
    Glob       mo.Option[string]
    Help       bool
}
func ParseArgs(argv []string) (ParsedArgs, error)
func ListDecisions(files []DecisionFile, f Filters, cfg ledgergate.Config) (lines, errs []string)
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int

// package dlreconcile
type LandedDecision struct{ ID, Surface, Ref string }
type ReconcileRequest struct { Repo string; Landed []LandedDecision }
type StaleClaim struct{ ID, Surface, Ref, Lane, Date string }
type DuplicateClaim struct { ID string; Surfaces []string }
type ReconcileResponse struct {
    Updated, Inserted int
    Stale             []StaleClaim
    Duplicates        []DuplicateClaim
}
func AssertReconcilableLedgers(cfg ledgergate.Config, files map[string]string) (ReconcileRequest, error)
func FormatReconcileOutput(r ReconcileResponse) string
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int
```

`--glob` uses `doublestar.Match`. `--grep` stays a case-insensitive substring
match, lowercased with `jstext.ToLower`.

### M9: `length-gate` comment ratchet

Port `config.ts` and the comment half of `core.ts` to `go/internal/lengthgate`,
and port jsdiff's array diff to `go/internal/linediff`. `wordsInReason` uses
`jstext.Fields`. Port the describe blocks `fixed policy and marker parsers`,
`comment ratchet` and `line pairing and failure output`, comment cases only.

Interfaces:

```go
// package lengthgate
const (Ceiling = 4; Marker = "LENGTH"; MinReasonWords = 3)
type DocClass struct {
    Name          string
    Include       []string
    Exclude       []string
    Target        float64
    MarkerAllowed bool
}
type Config struct { Extensions, Exclude []string; Docs []DocClass }
type CommentMarkerResult struct {
    Found  bool   // false means the line holds no marker
    Reason string // set when Found and Error is ""
    Error  string // set when Found and the marker is malformed
}

func LoadConfig(path string) (Config, error)
func CommentMarker(firstLine string) CommentMarkerResult
func LineMap(before, after string) func(headLine int) (int, bool)

// package linediff
type Op struct { Kind byte; Count int } // '=', '+' or '-'
func Lines(before, after []string) []Op
```

`LineMap` keeps the TS fallback that pairs unique lines through
`fallbackCommentKey`. The comment `Finding` and `formatFinding` comment line
are goldens.

### M10: `length-gate` document ratchet

Port the document half of `core.ts`: `proseWords`, `docMarker`,
`normalizeHeading`, `htmlText`, `inlineText`, `proseBlocks`, `judgeFile` and
`formatFinding`. Port the `doc ratchet` describe block and the rest of
`line pairing and failure output`. Capture TS goldens for `proseWords` (with
and without errata) and `docMarker` over the meissa `.md` files and the docs
input set (decision 8).

Word count: `proseWords` in `packages/length-gate/core.ts` strips one BOM
("`markdown.replace(/^\uFEFF/u, "")`"), joins the prose blocks with `\n`, then
runs "`.split(/\s+/u).filter((token) => /[\p{L}\p{N}]/u.test(token)).length`".
Go uses `jstext.Fields` for the split and counts tokens that hold at least one
rune in `unicode.L` or `unicode.N`.

Parser setup: `goldmark.New` with `extension.GFM`, `extension.Footnote` and
`&frontmatter.Extender{Formats: []frontmatter.Format{frontmatter.YAML}}`. The
frontmatter default also accepts TOML `+++`, which the TS does not.

Node map from mdast to goldmark, for the word walker:

| mdast (TS) | goldmark (Go) | Go rule |
| --- | --- | --- |
| `text` | `ast.Text` | decode the raw segment with `util.UnescapePunctuations`, then `util.ResolveEntityNames`, then `util.ResolveNumericReferences` |
| soft line break inside `text` | `ast.Text` with `SoftLineBreak()` | append `\n` |
| `break` | `ast.Text` with `HardLineBreak()` | append a space |
| `inlineCode` | `ast.CodeSpan` | its child text, not decoded |
| `link` from an autolink or GFM literal | `ast.AutoLink` | empty string |
| `link` with a `[label]` | `ast.Link` | its children |
| `image`, `imageReference` | `ast.Image` | its child text as the alt |
| `footnoteReference` | `extension/ast.FootnoteLink` | empty string |
| `footnoteDefinition` | `extension/ast.Footnote` | visit children |
| `html` (inline) | `ast.RawHTML` | `htmlText` of its segments |
| `html` (block) | `ast.HTMLBlock` | `htmlText` of its lines |
| `code` | `ast.FencedCodeBlock`, `ast.CodeBlock` | skip |
| `table` | `extension/ast.Table` | skip |
| `definition` | none (kept in the parser context) | nothing to skip |
| `yaml` | none (removed by the extension) | see below |

Lines and positions: a block's line is the line of the first segment in its
`Lines()`, mapped through a line-start offset table. goldmark keeps no
front-matter node, so `docMarker` finds the front-matter end line with a scan
that follows micromark's rule: line 1 is `---`, and the block ends at the next
line that is exactly `---`.

`normalizeHeading` uses `jstext.ToLower`, `jstext.Trim` and an `IsSpace` loop
for whitespace runs. Each `jstext` difference (NBSP, U+FEFF, word-final sigma,
U+0130) has a `proseWords` or `normalizeHeading` test.

Interfaces (package `lengthgate`):

```go
type Finding interface{ isFinding() }
type CommentFinding struct { Path string; Line, Lines, BaseLines int }
type DocFinding struct { Path, DocClass string; Words, BaseWords int; Target float64 }
type BadMarkerFinding struct { Path string; Line int; Reason string }
type UnscannableFinding struct{ Path string }
type Note struct { Path string; Line int } // unneeded marker

type DocMarkerResult struct {
    Found   bool
    Section string
    Reason  string
    Error   string // set when Found and the marker is malformed
}

func ProseWords(markdown string, dropErrata bool) int
func DocMarker(text string) DocMarkerResult
func JudgeFile(cfg Config, file commentscan.FileDiff, before, after string) ([]Finding, []Note)
func FormatFinding(f Finding, cfg Config) []string
```

### M11: `length-gate` git, runner and CLI

Port `git.ts`, `run.ts` and `index.ts` to `go/internal/lengthgate` and
`go/cmd/length-gate`. Port `runtime.test.ts`; its "CLI end to end" test runs
the built Go binary. Add `cmd/length-gate` to `tools`. Delete
`packages/length-gate`. After this release, the `tools` package in the meissa
flake carries `length-gate`; that is the "M9 to M11" a consumer waits on.

Interfaces (package `lengthgate`):

```go
type GateEnv struct {
    Event       string // "pull_request" or other
    Base        mo.Option[string]
    Fetch       mo.Option[string]
    Head        string
    Merge       bool
    All         bool
    SkipClasses []string
}
type Deps interface {
    Root() string
    Resolve(ctx context.Context, rev string) (string, bool, error)
    FetchBase(ctx context.Context) error
    MergeBase(ctx context.Context) (string, error)
    Diff(ctx context.Context, base, head string) (string, error)
    ReadAt(ctx context.Context, rev, path string) (string, bool, error)
    ListFiles(ctx context.Context, rev string) ([]string, error)
}

func NewGitDeps(root string, stdout, stderr io.Writer, head string) Deps
func GitTopLevel(ctx context.Context, root string) (string, error)
func RunOnce(ctx context.Context, deps Deps, cfg Config, env GateEnv, stdout, stderr io.Writer) int
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int
```

The CLI keeps `--config <path>`, `--all` and `--skip-class <name>`, every TS
usage error, and the env `GATE_ROOT`, `LENGTH_GATE_EVENT`, `LENGTH_GATE_BASE`,
`LENGTH_GATE_FETCH`, `LENGTH_GATE_MERGE` and `LENGTH_GATE_HEAD`. Shallow
deepening stops once head shares history with base, as in `createGitDeps`.

### M12: retire `renovate-preflight`

Delete `packages/renovate-preflight`, its release-please entries and its
`package-bins.test.ts` row (decision 5).

### M13: root checks in Go

Port `ref-gate.config.test.ts`, `rumdl-extends.test.ts` and
`shared-configs.test.ts` to `go/internal/repochecks`. Delete
`package-bins.test.ts`; no npm bin is left. The rumdl test reads
`RUMDL_BASE_CONFIG` and `RUMDL_BIN` and checks that `exec.LookPath("rumdl")`
equals `RUMDL_BIN`. The shared-configs test runs `node` and `biome` as child
processes. Point the root `biome.json` `extends` at
`./packages/biome-config/biome.json`, so linting no longer needs `bun
install`. The root `test` moon task then runs only Go.

Interfaces (package `repochecks`, test-only):

```go
func repoRoot(t *testing.T) string // walks up from the test file to the directory holding flake.nix
```

### M14: npm publish tool in Go, Bun leaves meissa

Port `tools/release/publish.ts` and its test to `go/internal/npmpublish` and
`go/cmd/npm-publish`. In `release.yml`, the step "Put Bun and Node.js on PATH"
today builds `nixpkgs#bun nixpkgs#nodejs`. It becomes "Put Go and Node.js on
PATH" and builds `.#go-dev nixpkgs#nodejs`, so the job uses the flake's pinned
`go_1_27`, never a runner Go. Node stays for `npm publish` and trusted
publishing. The step "Install workspace dependencies" is removed, and "Publish
released packages" runs `go -C go run ./cmd/npm-publish` with the same
`PUBLISH_ALL` env. Set `GOTOOLCHAIN=local`, `GOWORK=off` and
`GOFLAGS=-mod=readonly` on that step. Remove `bun` from `devenv.nix` and CI;
remove the root `package.json` workspaces, `bun.lock`, `bunfig.toml`,
`tsconfig.base.json`, `tools/golden/capture.ts`, the `bun` Renovate manager
and the `@types/bun` rules. Keep the `npm` manager for the two config
packages.

Interfaces (package `npmpublish`):

```go
type Manifest struct{ Name, Version string }
type Deps interface {
    Read(path string) (Manifest, error)
    Status(ctx context.Context, url string) (int, error)
    Publish(ctx context.Context, path string) error
    Log(line string)
}

func ParsePaths(raw string) ([]string, error) // "bad paths_released"; then keeps only packages/ paths
func IsPublished(ctx context.Context, m Manifest, d Deps) (bool, error)
func PublishAll(ctx context.Context, paths []string, d Deps) error
func Main(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int
```

### M15: RigelBuild/docs cutover

Starts after M3, M5, M7 and M8; docs uses no `length-gate`. In RigelBuild/docs
(decision 7):

- Bump the `meissa` flake input to a revision that includes M8. Add
  `tools = meissa.packages.${system}.tools` and
  `go-dev = meissa.packages.${system}.go-dev` to the flake, and both to
  `devenv.nix`.
- `ci.yml` and `deploy.yml` put `tools` and `go-dev` on `PATH` the way they put
  `bun` there today.
- CI runs `design-ledger-gate --config design-ledger.config.json` and
  `ref-gate --config ref-gate.config.json` from `tools`, with the same env
  (`GH_TOKEN`, `REPO`, `PR_NUMBER`). The `claim` script becomes the
  `dl-claim --config design-ledger.claim.config.json` binary.
- Port `site/scripts/gather.ts` and `gather.test.ts` to a Go module at
  `site/gather/` (module `github.com/RigelBuild/docs/site/gather`, directive
  `go 1.26.0`). It imports `ledger.ParseDecisionFile` and
  `ledger.RenderDecisionIndex`. Its slug function reproduces `github-slugger`
  and carries the ISC notice.
- `package.json` scripts: `gather` becomes `go -C site/gather run .`, and
  `build` keeps `bun run gather && astro build --root site`. `deploy.yml`
  needs no other change, because it runs `bun run build`.
- Port `site/scripts/ref-gate-config.test.ts` to a Go test that runs the
  `ref-gate` binary on fixture files.
- Drop `@rigelbuild/design-ledger-gate`, `@rigelbuild/dl-claim`,
  `@rigelbuild/ref-gate` and `github-slugger` from `package.json`.

Interfaces: consumes `ledger` at the first `go/v*` tag that contains M5, and
`tools` at the M8 revision.

### M16: npm deprecation

The trigger is a checklist on RIG-5901 with one row per consumer and package,
named generically ("consuming repository 1: length-gate"). A row is done when
that consumer's main no longer installs the package. When every row for a
package is done, Nemo runs
`npm deprecate @rigelbuild/<pkg> "Moved to Go: github.com/RigelBuild/meissa/go and the meissa flake tools package"`
for it. The packages are `comment-scan`, `design-ledger-gate`, `dl-claim`,
`dl-list`, `dl-reconcile`, `length-gate`, `ref-gate` and `renovate-preflight`.
File it as a human-action issue with the exact list (decision 6). Update
the meissa `README.md` install section to the flake `tools` package and
`go get`.

### Consumer contract

- Consumers wait on **M2 and M9 to M11**. M2 makes `commentscan` importable at
  the first `go/v*` tag (expected `go/v0.1.0`; the exact tag is recorded on
  RIG-5901). M11 makes `length-gate` available in the flake `tools` package.
- Then they add the flake `tools` package to their CI toolchain. Their
  length-gate job runs the Go binary with the same flags and env as the TS one.
- `design-ledger-gate` and the `dl-*` tools are ready after M7 and M8, with the
  same flags and env.
- "Same flags and env" means the parity baseline below, the last npm version in
  `.release-please-manifest.json`. A consumer on an older version migrates its
  config first. `design-ledger-gate` 2.0.0 replaced `exemptBranchPrefixes` and
  `exemptAuthors` with `exemptBranches`, so a 1.x config does not load.

| Tool or package | Parity baseline |
| --- | --- |
| `commentscan` | `comment-scan` 1.0.1 (1.0.0 plus the RIG-5717 Astro fix) |
| `design-ledger-gate` | 2.0.1 |
| `dl-claim` | 1.1.1 |
| `dl-list` | 1.0.1 |
| `dl-reconcile` | 1.0.3 |
| `length-gate` | 1.0.2 (1.0.1 plus the `comment-scan` 1.0.1 pin from M2) |
| `ref-gate` | 1.0.1, except the `v` flag (M3) |

## Tasks

- [ ] M1a Go module, moon, devenv, CI `go-dev`, `diff.go`
- [ ] M1b `commentscan` scanners part 1 (scan, languages, comment blocks, routing)
- [ ] M2 `commentscan` Markdown, TSX and Astro; release-please `go` package; first `go/v*` tag
- [ ] M3 flake `tools` package and `ref-gate`
- [ ] M4 Renovate hash refresh in Go, `gomod` manager, Renovate config test
- [ ] M5 `ledger` public package
- [ ] M6a `design-ledger-gate` pure core
- [ ] M6b `design-ledger-gate` runner and validation legs
- [ ] M7 `design-ledger-gate` claim check and CLI in `tools`
- [ ] M8 `dl-claim`, `dl-list`, `dl-reconcile` in `tools`
- [ ] M9 `length-gate` comment ratchet
- [ ] M10 `length-gate` document ratchet
- [ ] M11 `length-gate` git, runner and CLI in `tools`
- [ ] M12 retire `renovate-preflight`
- [ ] M13 root checks in Go
- [ ] M14 npm publish tool in Go; Bun leaves meissa
- [ ] M15 RigelBuild/docs cutover
- [ ] M16 npm deprecation
