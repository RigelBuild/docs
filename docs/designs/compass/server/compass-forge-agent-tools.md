# Server-side forge tools replace `gh` in the agent image

Tracking: RIG-5819. Ruling: Matt, 2026-10-19, on RIG-5900: server-side forge tools replace every
agent use of `gh`, and `gh` leaves the image; the owner-aware wrapper first proposed here is
dropped. Parent: [Multi-installation forge credentials](multi-installation-forge-credentials.md).
Companion: [Compass agent VCS tools](../agent-vcs-tools.md) (T9 removes jj-vine).

Ledger-impact: DL-588 files the tools and supersedes the T9 clause of the companion that keeps
`api.github.com` "for fetch and `gh` reads". DL-589 supersedes the DL-563 clause that the helper
"falls through to hosts.yml only for the default owner"; DL-563 stays Active. DL-633 (OQ-4) and
DL-634 (OQ-5) follow the recommendations and change before merge if ruled against. Freezes on
merge.

Unless a path says otherwise, every path is in
[RigelBuild/compass](https://github.com/RigelBuild/compass).

## Open Questions

None depends on another. The plan follows each recommendation.

1. **OQ-1, re-running a check.** The operator's skills re-run a stale `cancelled` check run that
   blocks merge-queue admission. The shipped `config/rules/no-retries.md` says "Do not rerun
   unchanged CI to make a flaky test or check pass".
   - (a) **Recommended.** No rerun tool; the agent reports the stale row and a human re-runs it.
     Cost: a queue bounce waits for a human.
   - (b) `forge_rerun_check`. Cost: Actions: write, and the retry the rule forbids.
   - (c) Fix the CI configuration that keeps superseded cancelled runs required. Cost:
     per-repository CI work.
2. **OQ-2, approval coverage.** `reviewDecision` survives a push, and `reviews[].commit_id` can
   read back as the new head.
   - (a) **Recommended.** `forge_get_pull_request` returns `approval_coverage` (`covered`,
     `stale`, `unknown`, `none`) from the timeline in position order. Cost: one paged read per
     agent PR read.
   - (b) A raw timeline tool. Cost: every agent re-derives coverage, which is easy to get wrong.
   - (c) Neither. Cost: agents keep trusting `reviewDecision`.
3. **OQ-3, `SECRET_KIND_GH` and `hosts.yml`.** After `gh` leaves, nothing reads `hosts.yml`.
   - (a) **Recommended.** Keep the wire kind, which carries the owner tokens; retire
     `GHHostsScript` and always run `removeGHHostsScript`. Cost: a `gh` name with no `gh`.
   - (b) Rename it `SECRET_KIND_FORGE`. Cost: an enum rename.
   - (c) Keep writing `hosts.yml`. Cost: a token file with no reader.
4. **OQ-4, read scope.** `gh` read with the brokered token, minted only for granted
   repositories. The Server reads with the whole installation's token and checks no read scope,
   so any agent could read logs, comments, and PRs of every installed owner.
   - (a) **Recommended.** Gate every forge read on `requireForgeScope`, answering `not_found`.
     Cost: reads outside the grants, such as an upstream public PR, stop working.
   - (b) Gate only files and logs. Cost: ungranted private comments and bodies stay readable.
   - (c) Keep reads ungated and document the reach. Cost: Matt accepts it explicitly.
5. **OQ-5, PR metadata without a push.** `vcs_submit` syncs and pushes before it writes, so
   promoting a reviewed draft or fixing a body can push an unreviewed head.
   - (a) **Recommended.** `forge_update_pull_request { ready?, title?, body? }`, author-only,
     never pushing; it also covers back-to-draft. Cost: a second writer of PR text.
   - (b) A metadata-only `vcs_submit` mode. Cost: a mode flag on the companion's tool.
   - (c) Re-review after every promote or body fix. Cost: a review round each time.

## Problem / Intent

A `gh` call uses the host-level credential, so it acts on the default installation even for
another owner's repository. Replace every `gh` use agents make with a `forge_*` tool that runs in
the Server through the owner's installation, then remove `gh` from the agent image.

## Approach

### Inventory

The shipped Compass config issues no `gh` command. Over the operator's agent skills and rules,
`grep -RhoE '\bgh (pr|api|run|auth|issue|release|search|workflow|repo)\b( [a-z-]+)?'` finds 73
hits in 24 files. Gateway MCP GitHub tools are outside this record; they keep their own
owner-blind credential.

| `gh` use | Hits | Covered by |
| --- | --- | --- |
| `pr ready`, `pr ready --undo` | 21 | `forge_update_pull_request` (OQ-5); `vcs_submit` `ready` when pushing |
| `pr edit` | 2 | `forge_update_pull_request` (OQ-5) |
| `pr create` | 5 | `vcs_submit` |
| `pr checks`, `api …/check-runs`, `…/status` | 15 + 4 | check rows, `forge_get_pull_request` |
| CI job log | shipped skill | `forge_get_check_log` |
| `pr view` comments, body, state | 7 | `forge_list_comments`, `forge_get_pull_request`, `vcs_pr_status` |
| `pr view --json commits` | in `pr view` | not covered: merge-queue trials only |
| `pr list`, `pr diff --name-only` | 7 + 1 | `forge_list_pull_requests`, `forge_list_pull_request_files` |
| `pr diff` | shipped skill | fetch `pull/<n>/head`, diff locally |
| `api …/commits/<sha>/pulls` | in `api` | `forge_list_pull_requests` `commit_sha` |
| `api graphql` thread state | 1 | thread fields, `forge_get_pull_request` |
| `api …/timeline` | in `api` | OQ-2; every PR opens as a draft |
| `run rerun`, `run list` | 2 | OQ-1 |
| `pr review` | 1 | `forge_submit_review` |
| `auth git-credential` | 1 | DL-589 |
| `issue`, `release`, `search`, `workflow`, `repo` | 0 | none |

### Tool set

Each tool runs through `ExecuteForgeCallAsAccount` in `go/server/forge.go`, under the owner
router (DL-564) and canonical wire types (DL-069).

| Tool | Approval | Forge call |
| --- | --- | --- |
| `forge_list_comments` | read | Top-level comments, newest page first. Also Linear. |
| `forge_list_pull_requests` | read | Shallow rows; `paths` and `commit_sha` filters. |
| `forge_list_pull_request_files` | read | Path, status, counts; no patch. |
| `forge_get_check_log` | read | The Actions job log tail of a check run. |
| `forge_update_pull_request` | write | Ready, draft, title, body; never pushes (OQ-5). |

`forge_get_pull_request` gains the body, one row per check with its check-run id, `outdated`
and `resolved_by` per thread, and `approval_coverage` (OQ-2). Only this arm calls the new
`ReviewCoverage`, so board ingestion pays nothing. An approval is `stale` when a push event
follows it, and `unknown` after an unmatched `review_dismissed`. Only comments work on Linear.

List rows omit checks, reviews, and threads, so a page is one request. `paths` answers "which
open PRs touch these paths" with one GraphQL `files(first: 100)` query per 100 PRs. Files carry
no patch: a reviewer fetches `pull/<n>/head` with its read credential and diffs locally.
Bodies, comments, and logs render inside `READ_FRAMING` with the per-body budgets.

### Read scope

With OQ-4 (a), every read arm runs `requireForgeScope` first, keeping the reach `gh` had.
`--forge-disable-scope-enforcement` turns both gates off.

### PR metadata without a push

With OQ-5 (a), `forge_update_pull_request` is scope-checked and author-only (D2 of the
companion). `ready` runs `markPullRequestReadyForReview` or `convertPullRequestToDraft`. A body
edit keeps the stack block and re-stamps the owner header. This tool owns draft state between
pushes; this record assumes `vcs_submit` without `ready` leaves draft state alone.

### Check logs

The tool parses the job id from the check run's `details_url` and follows the log redirect,
signed for one minute; Go drops `Authorization` on the cross-host hop. It sends
`Range: bytes=-<max_bytes>` and streams only when the reply is not 206, so a large log does not
hit the 30 s client timeout. A non-Actions check fails `failed_precondition`.

The App gains Actions: read. An installation keeps its old permissions until its owner approves,
so logs can work for one owner and not another. A 403 whose `X-Accepted-GitHub-Permissions` names
`actions=read` maps to `failed_precondition` naming the owner. Actions: read also exposes workflow
artifacts to the Server's App key; the agent token's explicit `gitCredentialPermissions` map keeps
it out of the agent.

### Credential cutover

Today `compass-forge-credential git-credential get` prints nothing for the default owner, and git
falls through to `gh auth git-credential`. After the cutover the helper serves every owner from
the file `GHOwnerTokensScript` already writes. A miss prints `quit=1`; a pathless request, unused
under `useHttpPath`, gets nothing.

A Server older than DL-563 sends an owner-less `SecretGH`, which reaches git today only through
`hosts.yml`. The materializer writes it to `~/.compass/forge/<host>/.host-default`, which the
helper serves when no owner file exists. The `.default-owner` marker, which could name a
non-default owner, loses its last reader with `token` and goes in T7. `token` served jj-vine,
which T9 of the companion removes; jj-vine#49 is needed only until then.

### Image and egress

`pkgs.gh` leaves `agent-image/toolchain.nix` after jj-vine, whose `tokenCommand` runs `gh`. The
egress allowlist keeps `github.com` for git and `api.github.com` for Nix, whose `github:` fetcher
resolves refs there. Only the runner's `-egress-allow` help text changes.

### Alternatives considered

- **A raw `forge_api` tool.** DL-069 bars raw forge shapes from the wire.
- **Patches on the wire.** `/pulls/{n}/files` caps at 3,000 files and drops large patches.
- **Check annotations instead of logs.** They need no new permission, but an Actions annotation
  is often only "Process completed with exit code 1".

## Plan

### Global Constraints

- Wire types are canonical `compass.v1` (DL-069); no raw forge struct crosses the gateway.
- `ForgeCallRequest` arms: `list_comments = 21`, `list_pull_requests = 22`,
  `list_pull_request_files = 23`, `get_check_log = 24`, `update_pull_request = 25`.
  `ForgeCallResult` arms 12-15. Main holds `edit_issue = 17`; the agent VCS tools line holds
  18-20 and result 11. The `client_request_id` comment becomes "oneof arms 2-11, 14-25".
- New Provider methods land on all four implementers; Linear returns `ErrUnsupported` except for
  comments.
- Every arm takes `caller` and runs `requireForgeScope` (OQ-4).
- The brokered git token gains no permission. Only the App gains Actions: read.
- Name no private repository or path.
- Tests first.

### T1. Provider reads and canonical fields

Lane: forge. Base: main.

Interfaces:

```go
// go/internal/forge/provider.go
type Comment struct{ /* existing */ IsBot bool; CreatedAt time.Time }
type ReviewThread struct{ /* existing */ Outdated bool; ResolvedBy string }
type Check struct{ /* existing */ CheckRunID uint64 } // 0 for a commit status
type CommentPage struct{ Comments []Comment; More bool } // ascending; More = older pages exist
type PullRequestFilter struct{ State string; Paths []string; CommitSHA string; Page int }
type PullRequestPage struct{ PullRequests []PullRequest; More bool } // shallow rows
type ChangedFile struct{ Path, PreviousPath, Status string; Additions, Deletions uint32 }
type FilePage struct{ Files []ChangedFile; More bool }
type CheckLog struct{ CheckRunID uint64; Name, Text string; Truncated bool }
type ApprovalCoverage string // "covered" | "stale" | "unknown" | "none"
type MissingPermissionError struct{ Repo, Accepted string } // X-Accepted-GitHub-Permissions

ListCommentPage(ctx context.Context, repo string, number uint64, page int) (CommentPage, error) // page 1 = newest
ListPullRequests(ctx context.Context, repo string, f PullRequestFilter) (PullRequestPage, error)
ListPullRequestFiles(ctx context.Context, repo string, number uint64, page int) (FilePage, error)
CheckRunLog(ctx context.Context, repo string, checkRunID uint64, maxBytes int) (CheckLog, error)
ReviewCoverage(ctx context.Context, repo string, number uint64) (ApprovalCoverage, error)
```

```proto
// proto/compass/v1/compass.proto
message PullRequest { /* 1-18 */ string body = 19; string approval_coverage = 20; }
message Check { /* 1-4 */ uint64 check_run_id = 5; }
message ReviewThread { /* 1-3 */ bool outdated = 4; string resolved_by = 5; }
message Comment { /* 1-3 */ string url = 4; google.protobuf.Timestamp created_at = 5; }
message ChangedFile { string path = 1; string previous_path = 2; string status = 3;
  uint32 additions = 4; uint32 deletions = 5; }
```

`ghCheckRun` decodes `id`. `pullReadQuery` adds `isOutdated resolvedBy { login }`. `translatePR`
keeps the stripped body. `ListCommentPage` starts from the `Link` `last` page.

1. A check run yields `CheckRunID`; a commit status yields 0.
2. A thread with `isOutdated` and `resolvedBy.login: "bot"` translates both.
3. `ListCommentPage` page 1 fetches only the last page and returns it ascending with `More`.
4. `Paths` returns only PRs touching a path, in one GraphQL call for 100 open PRs; `CommitSHA`
   returns the `PullRequestForSHA` hit.
5. `CheckRunLog`: a `details_url` without `/job/` fails precondition; a cross-host 302 is followed
   without `Authorization`; a 206 is used as is; a 200 keeps the last `maxBytes` and sets
   `Truncated`.
6. A 403 with `X-Accepted-GitHub-Permissions: actions=read` returns `MissingPermissionError`.
7. `ReviewCoverage`: approval then `committed` is `stale`; `committed` then approval is
   `covered`; an unmatched `review_dismissed` is `unknown`.
8. Linear returns `ErrUnsupported` for all but `ListCommentPage`.

### T2. Read arms and the read gate

Lane: forge. Base: T1.

Interfaces:

```proto
// proto/compass/v1/agent_gateway.proto, ForgeCallRequest.call
ListCommentsRequest list_comments = 21;   // {repo=1, kind=2 ForgeArtifactKind, number=3, page=4}
ListPullRequestsRequest list_pull_requests = 22; // {repo=1, state=2, repeated paths=3, commit_sha=4, page=5}
ListPullRequestFilesRequest list_pull_request_files = 23; // {repo=1, pr_number=2, page=3}
GetCheckLogRequest get_check_log = 24;    // {repo=1, check_run_id=2, max_bytes=3}
// ForgeCallResult.result
ListCommentsResponse comments = 12;          // {repeated compass.v1.Comment comments=1; bool more=2}
ListPullRequestsResponse pull_requests = 13; // {repeated compass.v1.PullRequest pull_requests=1; bool more=2}
ListPullRequestFilesResponse files = 14;     // {repeated compass.v1.ChangedFile files=1; bool more=2}
CheckLog check_log = 15;                     // {check_run_id=1, name=2, text=3, truncated=4}
```

```go
// go/server/forge.go
func (s *forgeService) listComments(ctx context.Context, caller store.AccountID, call *compassv1internal.ForgeCallRequest, req *compassv1internal.ListCommentsRequest) *compassv1internal.ForgeCallResult
func (s *forgeService) listPullRequests(ctx context.Context, caller store.AccountID, call *compassv1internal.ForgeCallRequest, req *compassv1internal.ListPullRequestsRequest) *compassv1internal.ForgeCallResult
func (s *forgeService) listPullRequestFiles(ctx context.Context, caller store.AccountID, call *compassv1internal.ForgeCallRequest, req *compassv1internal.ListPullRequestFilesRequest) *compassv1internal.ForgeCallResult
func (s *forgeService) getCheckLog(ctx context.Context, caller store.AccountID, call *compassv1internal.ForgeCallRequest, req *compassv1internal.GetCheckLogRequest) *compassv1internal.ForgeCallResult
// getIssue, listIssues, and getPullRequest gain caller and the scope check.
```

`getPullRequest` sets `approval_coverage` from `ReviewCoverage`. `max_bytes` defaults to 65536,
capped at 262144. `mapForgeError` maps `MissingPermissionError` to `failed_precondition`.

1. An empty `repo` on each arm is `invalid_argument`.
2. An agent granted `a/one` reading `b/two` gets `not_found` on every read arm, with no provider
   call; with enforcement off, the read runs.
3. A comment stamped by agent `a1` returns author `a1` and a clean body.
4. `MissingPermissionError` returns `failed_precondition` naming the owner and Actions: read.
5. A Linear selector on the PR arms returns `unimplemented`.

### T3. Agent read tools, render, and prompts

Lane: agent. Base: T2.

Interfaces:

```ts
// packages/compass-agent/src/forge.ts
export const listCommentsParameters = type({ ...forgeSelector, repo: nonBlank(REPO_DESC), kind: type("'issue' | 'pull_request'"), number: type("number.integer >= 1"), "page?": type("number.integer >= 1") });
export const listPullRequestsParameters = type({ ...forgeSelector, repo: nonBlank(REPO_DESC), "state?": type("'open' | 'closed' | 'all'"), "paths?": type("string[]"), "commit_sha?": type("string"), "page?": type("number.integer >= 1") });
export const listPullRequestFilesParameters = type({ ...forgeSelector, repo: nonBlank(REPO_DESC), pr_number: type("number.integer >= 1"), "page?": type("number.integer >= 1") });
export const getCheckLogParameters = type({ ...forgeSelector, repo: nonBlank(REPO_DESC), check_run_id: type("number.integer >= 1"), "max_bytes?": type("number.integer >= 1024") });
export function createForgeTools(broker: ForgeBroker): AgentTool[]; // four more read tools
```

`renderPrRecord` renders the body through `renderBody`, `[check <fence>] name= state= required=
id=` per check, `outdated= resolved_by=` per thread, and `approval_coverage=`. Logs use
`framedRead`. `SCOPE_DISCIPLINE` drops "there is no server-side scope check". A description rule
says the forge tools are the only forge path and the image has no `gh`. Update
`config/prompts/{manager,owner,supervisor}/SYSTEM.md`, the diff step in
`config/skills/review/SKILL.md` (fetch the PR ref), the overlap step in `config/skills/jj/SKILL.md`
(`paths`), `docs/concepts/tools.md`, and `packages/compass-agent/AGENTS.md`.

1. Each tool sends its arm and renders its result arm; a wrong arm throws `forgeFailure`.
2. A PR with 3 checks renders 3 rows and the rollup.
3. A log containing the fence string cannot close the fence.

### T4. `forge_update_pull_request`

Lane: forge. Base: T3, and T2 and T5 of the agent VCS tools design.

Interfaces:

```proto
// proto/compass/v1/agent_gateway.proto, ForgeCallRequest.call; returns pull_request = 5
UpdatePullRequestRequest update_pull_request = 25; // {repo=1, pr_number=2, optional bool ready=3, optional string title=4, optional string body=5}
```

```go
// go/internal/forge/provider.go
type UpdatePR struct{ /* existing Title, Body, BaseRef, Ready */ Draft bool } // runs convertPullRequestToDraft
// go/server/forge.go
func (s *forgeService) updatePullRequest(ctx context.Context, caller store.AccountID, sessionID string, call *compassv1internal.ForgeCallRequest, req *compassv1internal.UpdatePullRequestRequest) *compassv1internal.ForgeCallResult
```

```ts
// packages/compass-agent/src/forge.ts; approval: "write"
export const updatePullRequestParameters = type({ ...forgeSelector, repo: nonBlank(REPO_DESC), pr_number: type("number.integer >= 1"), "ready?": "boolean", "title?": nonBlank("New title"), "body?": nonBlank("New body") });
```

A body keeps the current stack block (`StampStack`) and is stamped with `StampOwner`.

1. An ungranted repo, and a PR the caller did not author, are refused before any forge call.
2. `ready: false` on a ready PR converts it to draft; `ready: true` on a draft marks it ready;
   `head_sha` is unchanged.
3. A body edit keeps the stack block and the owner header.
4. A call with no field set is `invalid_argument`.

### T5. App permissions, docs, and spec

Lane: forge. Base: T4.

Add a "GitHub App permissions" table to `docs/self-host.md`: Issues, Pull requests, and Contents
write; Checks, Commit statuses, Metadata, and Actions read. Mark which the agent token also gets.
Say each installation owner must approve the update, and that an egress-filtered Server must reach
the log redirect's storage host. In "Forge write scopes", replace "Reads are not gated." with
the read gate. Replace the `gh` sentence and its `token` example.

Add `docs/specs/product/compass/forge-tools.md`, linked from the spec README: "Agent forge access
runs in the Server under the agent's grants". Scenarios: a second owner's granted repository uses
that owner's installation; an agent granted `a/one` reading `b/two` gets `not_found`; a log read
without Actions: read fails `failed_precondition`; an update to another agent's PR is refused.

1. `rumdl` and the spec length check pass on the new spec.

### T6. Credential helper serves every owner

Lane: forge. Base: main.

Interfaces:

```go
// go/cmd/compass-forge-credential/main.go
func gitCredential(home string, args []string, in io.Reader, out, stderr io.Writer) error // owner file, else .host-default, else quit=1
// go/internal/forgecred/forgecred.go
func HostDefaultPath(home, host string) string // ~/.compass/forge/<host>/.host-default
// go/internal/runtime/secrets_materialize_owner.go
func GHOwnerTokensScript(homeDir string, creds []GHCredentials) (string, error) // owner "" writes HostDefaultPath
```

`Install` passes every `SecretGH` to `GHOwnerTokensScript`. Remove the `gh auth git-credential`
helper line and its comment from `agent-image/toolchain.nix`. `token` keeps its `gh` fallback
until T7.

1. `get` for the default owner prints its token.
2. With only an owner-less credential, `get` for any owner prints that token.
3. A miss with owner files present prints `quit=1` and `no credential for owner <x>`.
4. A pathless `get` prints nothing.
5. `TestAgentImageGHTokenAuth` "default owner falls through" now expects the owner file's token,
   and fails closed when that file is gone.

### T7. Remove `gh` from the image

Lane: forge. Base: T3, T4, T6, and T9 of the agent VCS tools design.

Remove `pkgs.gh` and the `gh` header and CA comments in `agent-image/toolchain.nix`, and the `gh`
comments in `agent-image/devenv.nix` and `agent-image/devenv.yaml`. Remove the `token`
subcommand, `runGHToken`, the `os/exec` import, `ghBinary`, `-X main.ghBinary` in
`tools/toolchain/compass-forge-credential.nix`, `forgecred.DefaultOwner`, `DefaultOwnerPath`, and
the `.default-owner` writes. Apply OQ-3. Change the `-egress-allow` help text to "git needs
github.com; Nix fetchers need api.github.com". Rename `TestAgentImageGHTokenAuth` to
`TestAgentImageGitCredential`, drop its `token` cases, and assert `gh` is absent from `PATH`.
Amend the agent-sessions spec credential requirement to name git only.

1. `command -v gh` fails in the image.
2. `compass-forge-credential token` exits 2 with `unknown command "token"`.
3. `Install` leaves no `hosts.yml` and no `.default-owner`.

## Tasks

- [ ] T1. Provider reads and canonical fields
- [ ] T2. Read arms and the read gate (OQ-4)
- [ ] T3. Agent read tools, render, and prompts
- [ ] T4. `forge_update_pull_request` (OQ-5)
- [ ] T5. App permissions, `docs/self-host.md`, spec
- [ ] T6. Credential helper serves every owner
- [ ] T7. Remove `gh` from the image, OQ-3, egress text
