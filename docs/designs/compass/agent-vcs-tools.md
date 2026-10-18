# Compass agent VCS tools — commit, stack, and submit without raw git or jj

Tracker: RIG-5558

> Freezes on merge; later changes supersede by citation, never rewrite
> (convention: the `docs/designs/agent/compass-agent-config-delivery/design.md`
> header). Unless a path says otherwise, every path is in
> [RigelBuild/compass](https://github.com/RigelBuild/compass).

Ledger-impact: DL-535..DL-538 file D1–D4. DL-558 supersedes the DL-142
clause that keeps `jj-vine submit` as the push path (T8). DL-559 supersedes
the OQ-10 clause of DL-442 (T9). DL-561 supersedes the DL-052 clause "The
agent keeps a push-scoped git credential" (OQ-8, T9, T10).

**Named decisions this record introduces:** D1–D4 in
[Decisions](#decisions).

## Rulings

Matt ruled on 2026-10-17 in the review threads of RigelBuild/docs PR #39.
No open questions remain.

- **OQ-1 — agent token.** If the agent keeps any forge token, it has
  `pull_requests: read`. Raw PR edits fail; `gh` PR reads work.
- **OQ-2 — RIG-1882.** Dropped. Under OQ-8 the agent holds only read
  scopes, so the forge refuses a raw push, merge, or PR write, and a push
  guard would only add a second refusal. The push guard draft
  (RigelBuild/docs PR #13, DL-507..DL-510) closes unmerged.
- **OQ-3 — issue check.** The Server checks that the issue exists at
  create. Accepted formats are the user's own: GitHub `#12` and
  `owner/repo#12`, and a Linear key for any team the user has configured.
  No team key is hard-coded (D3).
- **OQ-4 — stack shape.** Linear only; `vcs_submit` refuses a fork (D4).
  GitHub's stacked-PR feature allows only linear lines.
- **OQ-5 — jj-vine cutover.** Close and resubmit. The cutover closes each
  open jj-vine PR and resubmits its line through the tools. The new PR body
  links the old PR; review history on the old PR is lost. There is no
  period where both paths run (T8, T9).
- **OQ-6 — another agent's PR in the line.** The Server manages the whole
  line. When a line holds a PR another agent authored under the same owner
  (DL-442: "grants are owner-level"), the Server rebases and pushes that
  bookmark and tells the owning agent. On a rebase conflict it pushes
  nothing and asks the owning agent to resolve.
- **OQ-7 — pre-push gate.** `vcs_submit` runs cheap format and lint checks
  through the repo's own configured hook runner, not a runner Compass picks.
  The Setup agent installs basic hooks in a repo that has none. CI on the
  PR is the main gate, because full suites do not fit small agent compute.
- **OQ-8 — push credential.** Server push. `vcs_submit` sends a git bundle
  of the own commits; the Server applies it to a per-repo mirror and pushes
  with its App token (T10). The agent keeps a read-only token for clone and
  fetch. Today the push token is a file secret
  (`brokeredSecretResolver.ResolveFor` in `go/server/git_credential.go`)
  written 0600 into the agent home, and a local OMP tool or stdio MCP server
  runs as the same UID as `bash`, so neither can protect it. The first hop
  caps a message at `maxAgentMessageBytes = agentmsg.MaxBytes` (16 MiB,
  `go/internal/runner/gateway/gateway.go`), so the tool refuses a
  serialized call over 15 MiB.
- **OQ-9 — pre-push time limit.** Fail open. After 120 s the tool stops the
  hook and pushes. The result tells the agent that the hook timed out and
  that it must find out why and fix the hook, in its own change, so the
  next submit runs it in time.

## Problem / Intent

Compass agents drive version control with raw jj, git, gh, and
`jj-vine submit`. They spend turns on VCS mechanics, PRs can lack an issue,
and a PR body can carry two stack blocks. These tools write the PR title and
body, require an issue per PR, and let the Server own one stack block.

## Global Constraints

- The agent gains no credential. Every PR write and every push goes through
  the Server forge broker (DL-052, DL-561).
- Every forge object the Server creates for an agent gets an ownership row,
  even when the create's own record step fails (D2 write intents).
- The Server alone writes the owner header (DL-050) and the stack block, and
  strips both from agent text.
- No `@connectrpc` import outside `packages/compass-agent/src/transport/**`.
- `jj` runs from an argument array with `--no-pager --color=never`; no editor.
- No agent tool rebases, tracks, or pushes another agent's bookmark; only
  the Server does (OQ-6 ruling).
- Write tools in one agent run one at a time.
- `vcs_*` are Manager tools; `subagent-tool-split.test.ts` is unchanged.
- PR numbers are `uint64` in Go, as in `forge.Provider`.
- Red → green: failing tests land first (`bun test`; Go `FakeProvider`,
  `pgtest`).
- Gate: `moon run :ci`; proto edits run `moon run compass-proto:gen`.
- T3 and T5 start after RigelBuild/compass#2354 (connect-go v2, RIG-5471)
  merges.
- Name no private repository or path.

## Approach

**D1 — hybrid split.** Local jj work runs in the agent
(`packages/compass-agent/src/vcs.ts`). Push and PR writes run in the
Server: the agent sends a bundle, and the Server pushes it from its mirror
(T10) and writes the PRs through three new `ForgeCallRequest` arms. Seven
tools:

| Tool | Approval | Does |
| --- | --- | --- |
| `vcs_status` | read | Stack from the base to the tip: change, commit, bookmark, subject, conflict, empty. |
| `vcs_commit` | write | Describes `@` (or `<workspace>@`) from fields, sets a bookmark, then `jj new <bookmark>`. |
| `vcs_workspace` | write | `add`, `forget`, or `update-stale` one subagent workspace. |
| `vcs_sync` | write | `jj git fetch`, then rebases the own commits onto the base (D4). Reports conflicts. |
| `vcs_submit` | write | Sync, D4 checks, pre-push hook, then `submit_stack` with a bundle of the own commits. |
| `vcs_retire` | write | Deletes one own local bookmark; the Server closes its PR and deletes that one remote branch. |
| `vcs_pr_status` | read | Calls `stack_status`: PR, state, draft, checks, and authorship per bookmark. |

Subagents edit files only. The Manager commits their work with
`vcs_commit {workspace}`, run in that workspace's root.

**How `vcs_*` and `forge_*` fit together.** The `vcs_*` tools own
branches and the PRs built from them. The `forge_*` tools in
`packages/compass-agent/src/forge.ts` keep issues, comments, reviews,
reads, and subscriptions:

| Need | Tool |
| --- | --- |
| Find or create the issue a PR needs | `forge_list_issues`, `forge_get_issue`, `forge_create_issue` |
| Push and create or update PRs | `vcs_submit` (`submit_stack`) |
| Close a PR and delete its branch | `vcs_retire` (`retire_head`) |
| Stack shape, checks, authorship | `vcs_pr_status` (`stack_status`) |
| Reviews, threads, and the full PR | `forge_get_pull_request` |
| Comment, review, follow a PR | `forge_comment_on_pull_request`, `forge_submit_review`, `forge_subscribe` |

T7 removes `forge_create_pull_request` from the agent toolset. Its
description says "head_ref must be a branch you ALREADY pushed with your
own git credential", and after T9 the agent cannot push. The
`create_pull_request` arm stays as the create step inside `submit_stack`.
`forge_transition_pull_request_state` stays for reopen. Like `vcs_retire`,
it is author-only after T5. Both tool families reach `ExecuteForgeCallAsAccount` in
`go/server/forge.go`, so scope checks, the owner header, and ownership rows
are the same for both.

**D2 — PR writes through Server arms.** `createPullRequest` in
`go/server/forge.go` creates on the forge first, then records:

```go
pr, err := rf.author.CreatePullRequest(ctx, req.GetRepo(), forge.CreatePR{
// ...
if fe := s.recordPullRequest(ctx, rf, id, caller, sessionID, call.GetClientRequestId(), req.GetRepo(), pr, wire, link); fe != nil {
```

So a failed record leaves a PR with no ownership row. `createIssue` has the
same gap, and comments and reviews get no row at all (the comment arms
"mint no artifact coordinate"). The fix is a write intent, and it covers
every Server create made for an agent. `mirror_messages` already recovers
through its own lease and `compass-mirror:` marker, so it is left as is.

1. Before the forge call, the Server commits a `forge_write_intents` row
   in its own transaction: caller, owner, session, `client_request_id`,
   coordinate, kind (issue, PR, comment, review), parent number, and head
   for a PR. The row is unique on `(agent, client_request_id)`. Every
   create needs a key: `forge.ts` already sends
   `broker.idempotencyKey(toolCallId)` for issue and PR creates, and T7
   sends it for comments and reviews too. A `submit_stack` entry uses
   `<call key>:<head_ref>`.
2. A retry with the same key gets the existing intent back, before any
   forge call. A done intent returns its object. A pending one runs step 6
   first. A failed one returns its failure; a fresh tool call has a fresh
   key and so a fresh intent.
3. The create carries the intent id, a UUID v4. A GitHub body ends with
   `<!-- compass:write <id> -->`, as `mirror.WithMarker` ends a mirror
   post, and the Server strips any such marker from agent text first. On
   Linear, the intent id is the create's own `id` input. `IssueCreateInput`
   takes "The identifier in UUID v4 format", and `CommentOnIssue` already
   sends `clientID` as the comment's `id`.
4. After success, one transaction writes the ownership row (issue and PR
   in `forge_authored_artifacts`; comment and review keep the forge key on
   the intent row) and marks the intent done.
5. When the forge refuses the create, the intent is marked failed. On a
   timeout, a 5xx, or a failed record, it stays pending.
6. A retry, or a Server sweep of pending intents older than one minute,
   looks for the object: a PR by head, a Linear object by id, and a GitHub
   issue, comment, or review by the marker, listing from the intent's
   time. A hit is recorded. On a miss, only a create that cannot make a
   duplicate runs again under the same intent: a PR, because GitHub allows
   one open PR per head, and a Linear object, because Linear refuses a
   second create with that id. A GitHub issue, comment, or review is never
   sent twice while its first outcome is unknown. The retry returns
   "pending: the Server is still checking the forge; retry later". The
   sweep marks the intent failed after one hour with no hit. Failed intents
   stay searched for a day; a late hit is still recorded on that intent, so
   a slow listing loses nothing.

`submit_stack` (field 17) takes entries bottom to top. Per entry:

1. Find the PR by head in any state, open first. The repo squash-merges and
   deletes merged branches, so a merged or closed hit refuses: "sync first".
2. On a miss, create a draft with the issue link through the
   `createPullRequest` steps, under a PR write intent.
3. On an unrecorded hit, adopt the PR only if a pending intent names the
   caller and the head: write the ownership row and the link, with no
   second create. Else the PR is foreign.
4. On an own hit, PATCH only changed fields: base, block, header, draft,
   and the text only on `replace_text`.

The first failed entry stops the call: later entries return `skipped`, and
no restamp runs. Otherwise the Server restamps each lower PR whose block
changed. Only `ready_head_ref` leaves draft. `stack_status` (field 18) is the
read twin.

Rows written here carry a NULL `client_request_id`.
`forge_authored_artifacts_request_memo_idx` is unique on
`(agent_account_id, client_request_id)`, so one call key would fail every
create after the first, after the forge create. The head lookup and the
intent are the idempotency.

Repo strings are canonical. The intent keys on `ForgeCoord.Normalized()`.
The ownership row stores the forge's `base.repo.full_name`, the string
webhooks key on (`wh.Repository.FullName`). `AuthoredArtifactByCoordinate`
matches `repo = $3` exactly, and `CreatePullRequestWithLink` "keeps the
caller's repo casing", so the new lookup folds GitHub case, as
`InsertPullRequestIfAbsent` does
(`CASE WHEN $1 = 1 THEN lower(s.repo) ELSE s.repo END`). When old rows
differ only in case, the oldest wins, as write-once authorship does.

Only the author updates a PR, with one exception (OQ-6 ruling): when a
foreign PR in the caller's line has an ownership row with the caller's
owner, the Server rebases that bookmark in its mirror, pushes it, restamps
only its block, and tells the owning agent. The notice is authored by the
submitting agent in its DM with the owning agent: `Comms.OpenDM` resolves
or creates it ("Same-owner ... are allowed"), and both agents are members.
It is posted the way `Comms.PostMessage` posts: `AppendMessage` stores it,
then `publishMessagePosted` publishes `MessagePosted`, so a live agent gets
it at once. A DM channel is born `mandatory_subscription`, so
`UndeliveredMessages` sweeps it for an offline agent, and a failed publish
is left to "the delivery recovery sweep". The notice names the bookmark,
the old and new head, and the PR. On a conflict the Server pushes nothing,
marks the entry failed, and sends the same kind of notice asking the
owning agent to resolve.
Any other foreign write is refused, as is every write to an unrecorded PR
the caller cannot adopt.

`vcs_retire` closes through `transition_pull_request_state`.
`transitionPullRequestState` goes from scope check to forge call with no
authorship check:

```go
if fe := s.requireForgeScope(ctx, caller, rf, req.GetRepo()); fe != nil {
// ...
pr, err := rf.author.TransitionPullRequestState(ctx, req.GetRepo(), req.GetPrNumber(), forge.TransitionState{State: req.GetState()})
```

T5 makes close fail closed: it runs only when an ownership row names the
caller. After the OQ-5 cutover every open PR was created by the tools, so a
PR with no row is a human's or a foreign App's, and refusing it is correct.
The cutover closes the old jj-vine PRs before T9, with the token and
`gh pr close` the agents hold today (T8).

**D3 — issue and text.** Each PR names its own issue: #2334
("Refs RIG-5511") stacks on #2333 (RIG-5510). `vcs_submit` takes a
bookmark-to-issue map, required for bookmarks with no PR. The Server parses
the user's ref into `PullRequestIssueLink`, whose `repo` is "owner/name;
the team key on Linear". `refs.Parse` reads a Linear key (`TEAM-12`, any
2–10 character team key) or `owner/repo#12`; the tool expands a bare `#12`
to the PR's own repo first. `resolveIssueLink` in `go/server/forge.go`
refuses a forge with no provider: "forge: no provider configured for the
issue's coordinate". The Server then reads the issue with `GetIssue` at
create and refuses unless the tracker returns it as an open issue. Not
found refuses, including a team key the user has not connected. A closed
issue refuses. A GitHub PR number also refuses: `/issues/{n}` returns PRs
too, so T1 carries `ghIssue.PullRequest` through to `forge.Issue`. A
tracker outage fails the entry as retryable and creates nothing, so no PR
is ever created against an issue nobody checked.

The tools push the commits as the agent made them. Nothing is squashed or
rewritten: the bundle holds every own commit, and each PR shows each of
its commits. Only the PR title and body come from one commit, the
segment's first, `roots(<below>..<bookmark>)`. The title is its subject
plus `(RIG-12)`; the body is its description plus `Refs <issue>`. Only a
first commit takes `sections`, one per `.github/PULL_REQUEST_TEMPLATE.md`
heading, checked against the template limits. Text is written at create
and kept until `vcs_submit {text}` replaces it, because the jj skill says
"Never amend a pushed commit".

**D4 — stack, sync, and one block.** The own commits are
`::<tip> ~ ::(trunk() | untracked_remote_bookmarks())`. The tip is the
`bookmark` input, else the one bookmark on `@` or `@-`. An untracked remote
bookmark is another agent's branch, immutable by jj's default; own commits
on one have a foreign base.

Sync runs `jj git fetch --remote origin`, then
`jj rebase -s 'roots(<own>)' -o <base> --skip-emptied`, where `<base>` is
`trunk()` or `<foreign>@origin`. In jj 0.45.1, `--skip-emptied` drops a
squash-landed commit and fetch does not follow an advanced foreign base.
Sync tracks a remote bookmark only if `stack_status` says the caller
authored its PR.

Before push the tool refuses conflicts, merges, empty descriptions, two
bookmarks on one commit, unpublished commits above the tip, a bookmark with
no PR and no issue, and a fork:
`(descendants(<own>) & bookmarks()) ~ ::<tip>` is not empty. The no-issue
refusal tells the agent to find an issue that fits with
`forge_list_issues`, or to create one with `forge_create_issue` when none
does, then submit again. The Manager prompt says the same (T8). It then runs
the repo's own pre-push hook with the inputs a real push gives it:
`git hook run --ignore-missing --to-stdin=<file> pre-push -- origin <url>`,
where the file holds one `<local ref> <local sha> <remote ref> <remote sha>`
line per own bookmark. The repo's hook runner decides what runs; OQ-9
bounds the time. `jj git push` runs no git hooks (jj 0.45.1, checked), and
the agent does not push at all (OQ-8), so the tool calls the hook.

`vcs_submit` then runs `git bundle create` for `<base>..<own bookmarks>`
and sends it with `submit_stack`. The Server fetches the bundle into its
mirror, checks that each bookmark points at the agent's own commit, and
pushes those exact commits with its App token before the PR writes. A
foreign base must already be on the forge.

Each bookmark is one PR, based on the bookmark below, the foreign base, or
trunk. The block sits between `<!-- compass:stack -->` and
`<!-- /compass:stack -->` below the owner header. Before each write the
Server strips every compass and jj-vine block in a loop, because
`remove_jj_vine_stack_from_description` in jj-vine matches only one pair.

**Alternatives.** An agent-side push keeps a write token on the agent UID
(OQ-8). Wrapping jj-vine keeps agent-token PR writes.

## Plan

### T1 — provider update and lookup

Interfaces (`go/internal/forge/provider.go`):

```go
type UpdatePR struct {
	Title   string
	Body    string
	BaseRef string
	Ready   bool
}

UpdatePullRequest(ctx context.Context, repo string, number uint64, u UpdatePR) (PullRequest, error)
FindPullRequestByHead(ctx context.Context, repo, headRef string) (PullRequest, bool, error)
// FindByWriteMarker looks for the object a write intent created.
FindByWriteMarker(ctx context.Context, repo string, kind WriteKind, parent uint64, intentID string, since time.Time) (WrittenRef, bool, error)
```

GitHub PATCHes `/repos/{repo}/pulls/{n}`; `Ready` runs GraphQL
`markPullRequestReadyForReview` with a new `NodeID` (`json:"node_id"`) on
`ghPullDetail`. Both `ghPull` (the create response, whose `Base` holds only
`Ref` today) and `ghPullDetail` gain `Base.Repo.FullName`
(`json:"full_name"`), surfaced as `PullRequest.RepoFullName`, so a create
records the canonical name with no extra read. Lookup:
`GET /repos/{repo}/pulls?state=all&head=<owner>:<ref>`; merged means
non-null `merged_at`. `forge.Issue` gains `IsPullRequest`, set from
`ghIssue.PullRequest`. `FindByWriteMarker` lists issues, issue comments, or
reviews created since the intent and matches `<!-- compass:write <id> -->`;
Linear reads the object by the intent id. `CreateIssue` gains an `ID`
field, which Linear sends as `IssueCreateInput.id`. Linear PR calls return
`ErrUnsupported`. Red → green (`httptest`): PATCH body, ready, hit, miss,
merged, `full_name` on create and on read, 422, a PR number read as an
issue, a marker hit and miss per kind, a Linear issue created with an id
and read back by it, a second Linear create with that id refused.

### T2 — stack block

Interfaces (`go/internal/forge/stack.go`):

```go
type StackLine struct {
	Number uint64
	Title  string
}

func RenderStack(lines []StackLine, self uint64) string
func StripStack(body string) string
func StampStack(body, block string) string
```

Red → green: two jj-vine pairs and one compass pair yield one block; an
unclosed marker stays text; `StampStack` is idempotent, header first.

### T3 — proto

Interfaces (`proto/compass/v1/agent_gateway.proto`):

```proto
message StackEntry {
  string head_ref = 1;
  string base_ref = 2;
  string title = 3;
  string body = 4;
  string issue_ref = 5;  // user's format; required when the PR is created
  bool replace_text = 6;
}
message SubmitStackRequest {
  string repo = 1;
  repeated StackEntry entries = 2;  // bottom to top
  string ready_head_ref = 3;        // empty = no entry leaves draft
  bytes bundle = 4;                 // git bundle of the own commits (OQ-8a)
}
message RetireHeadRequest {
  string repo = 1;
  string head_ref = 2;
}
message StackStatusRequest {
  string repo = 1;
  repeated string head_refs = 2;
}
enum StackEntryStatus {
  STACK_ENTRY_STATUS_UNSPECIFIED = 0;
  STACK_ENTRY_STATUS_DONE = 1;
  STACK_ENTRY_STATUS_FAILED = 2;
  STACK_ENTRY_STATUS_SKIPPED = 3;  // an entry below failed
}
message StackEntryResult {
  string head_ref = 1;
  compass.v1.PullRequest pull_request = 2;
  bool created = 3;
  bool authored = 4;  // the caller authored this PR
  ForgeCallError error = 5;
  StackEntryStatus status = 6;
}
message StackResult {
  repeated StackEntryResult entries = 1;
}
```

`ForgeCallRequest` adds `submit_stack = 17`, `stack_status = 18`,
`retire_head = 19`; `ForgeCallResult` adds `stack = 11`. The
`client_request_id` comment ("oneof arms 2-11, 14-16") becomes
`2-11, 14-19`. Red → green: buf gates.

### T4 — store: write intents and case-folded lookup

Interfaces (`go/internal/store/forge_write_intents.go`):

```go
type WriteIntent struct {
	ID             string
	Provider       ForgeProvider
	Host           string // ForgeCoord.Host, as forgeCoordinate resolves it
	Repo           string // ForgeCoord.Normalized()
	Kind           WriteKind // issue, PR, comment, review
	Parent         uint64    // issue or PR number for a comment or review
	HeadRef        string    // PR only
	AgentAccountID AccountID
	OwnerUserID    AccountID
	SessionID      string
	RequestKey     string // the call's client_request_id; required
}

// Same (agent, RequestKey): returns the stored row. A PR intent: ErrConflict when another agent holds the head.
func (s *Store) RecordWriteIntent(ctx context.Context, in WriteIntent) (WriteIntent, error)
// ErrNotFound on a miss.
func (s *Store) PRIntentForHead(ctx context.Context, provider ForgeProvider, host, repo, headRef string) (WriteIntent, error)
// One transaction: the ownership row (issue, PR) or the forge key (comment, review), then done.
func (s *Store) CompleteWriteIntent(ctx context.Context, id string, a *AuthoredArtifact, forgeKey string) error
func (s *Store) FailWriteIntent(ctx context.Context, id string) error
func (s *Store) PendingWriteIntents(ctx context.Context, olderThan time.Time, limit int) ([]WriteIntent, error)
// Folds GitHub case; the oldest row wins.
func (s *Store) AuthoredArtifactByCoordinateFold(ctx context.Context, provider ForgeProvider, host, repo string, kind ForgeArtifactKind, number uint64) (AuthoredArtifact, error)
```

Table `forge_write_intents` in `migrations/0001_init.sql`: PK
`(tenant_id, id)`; state pending, done, or failed; `forge_key` and
`created_at`; a unique index on `(tenant_id, agent_account_id,
client_request_id)`, and one on `(tenant_id, forge_provider, forge_host,
repo, head_ref)` for PR rows; the `forge_authored_artifacts` agent/owner
FK; RLS. A done comment or review row is that object's ownership record.
sqlc queries go in `queries/forge_write_intents.sql`. `RecordWriteIntent`
commits on its own. Existing rows need no backfill. Red → green
(`pgtest`): another agent's PR intent for a head conflicts; the same key
returns the same row with the same id; an empty key is refused; the same
repo and head on two hosts are two
intents; complete writes the row and the state together or neither;
pending lists only old pending rows; mixed case finds the authored row;
two rows that differ only in case return the oldest.

### T5 — Server arms and write intents

Interfaces (`go/server/forge.go`, `go/server/forge_write_sweep.go`):

```go
func (s *forgeService) submitStack(ctx context.Context, caller store.AccountID, sessionID string, call *compassv1internal.ForgeCallRequest, req *compassv1internal.SubmitStackRequest) *compassv1internal.ForgeCallResult
func (s *forgeService) stackStatus(ctx context.Context, caller store.AccountID, call *compassv1internal.ForgeCallRequest, req *compassv1internal.StackStatusRequest) *compassv1internal.ForgeCallResult
func (s *forgeService) retireHead(ctx context.Context, caller store.AccountID, call *compassv1internal.ForgeCallRequest, req *compassv1internal.RetireHeadRequest) *compassv1internal.ForgeCallResult
// sweepWriteIntents runs each minute and resolves pending intents (D2).
func (s *forgeService) sweepWriteIntents(ctx context.Context) error
```

`forgeStore` adds the T4 methods. `createIssue`, `createPullRequest`,
`commentOnIssue`, `commentOnPullRequest`, and `submitReview` write under an
intent (D2). `transitionPullRequestState` gains the D2 authorship refusal.
`submitStack` resolves `issue_ref` and checks it with `GetIssue` (D3).
Red → green (`FakeProvider`, T10 fake mirror):

- A new entry without `issue_ref` fails; two entries make two drafts with
  chained bases; a retry creates nothing; a merged head refuses.
- `TEAM-12` for an unconfigured team, a not-found issue, a closed issue,
  and a PR number refuse; an outage fails the entry as retryable with no
  create.
- Two new entries under one call key: two PRs, two rows with a NULL key.
- For each create kind, the forge create succeeds but its response is
  lost, or the store write after it fails: a retry with the same key, or
  the sweep alone, ends with one object on the forge and one ownership
  record; an agent-written `compass:write` marker is stripped.
- A create the forge refused leaves a failed intent; a pending intent with
  nothing on the forge after one hour turns failed.
- A GitHub issue create whose response is lost, with the marker not yet
  listed: the retry returns pending and sends no second create; the sweep
  records the one issue once it lists.
- An unchanged entry sends no PATCH; an agent-written block is replaced.
- A mid-stack 429: earlier entries done, that entry failed, later entries
  skipped with no provider call.
- Only `ready_head_ref` leaves draft.
- A same-owner foreign PR in the line is rebased, pushed, and restamped;
  the notice, authored by the submitting agent in their DM, reaches a live
  owning agent as `MessagePosted` and an offline one in its undelivered
  sweep; a conflict pushes nothing and asks that
  agent to resolve; a different-owner foreign PR is refused.
- A same-scope close of a PR another agent authored, and of a PR with no
  row, is refused with no provider call; the author's own close runs.
- `retireHead` closes an own PR and deletes only that branch; a retry after
  either step finishes with no second close; a foreign head refuses.

### T6 — jj runner and local tools

Interfaces (`packages/compass-agent/src/vcs.ts`):

```ts
export interface JjResult { stdout: string; stderr: string; code: number }
export interface JjRunner {
  run(args: readonly string[], cwd: string): Promise<JjResult>;
}
export interface VcsDeps { jj: JjRunner; cwd: string; forge: ForgeBroker }
export function ownCommits(tip: string): string; // D4 revset
export function createVcsTools(deps: VcsDeps): AgentTool[];
// vcs_status    { bookmark?: string; workspace?: string }
// vcs_commit    { bookmark: string; subject: string; sections?: Record<string, string>; text?: string; workspace?: string }
// vcs_workspace { action: "add" | "forget" | "update-stale"; name: string; base?: string }
// vcs_sync      { bookmark?: string }
```

Rows come from one `jj log --no-graph` call with a `json(self)` template.
Red → green (fake `JjRunner`): exact argument arrays; a first commit missing
a heading fails; `vcs_commit` ends with `jj new <bookmark>`; fetch precedes
the rebase; a foreign base is never a rebase source; a conflict is reported.

### T7 — submit, retire, PR status, and wiring

```ts
// vcs_submit    { bookmark?: string; issues?: Record<string, string>; ready?: boolean;
//                 text?: { bookmark: string; subject: string; sections: Record<string, string> } }
// vcs_retire    { bookmark: string }
// vcs_pr_status { bookmark?: string }
```

`vcs_submit` syncs, checks, runs the pre-push hook (D4, OQ-9), builds the
bundle, then calls `submit_stack` (`ready_head_ref` = tip on `ready`); it
refuses trunk and a serialized call over 15 MiB. A hook past 120 s is
stopped and the push goes on; the result says "pre-push hook timed out
after 120 s: find out why and fix the hook in a change of its own".
`vcs_retire` calls `retire_head`, then deletes the local bookmark and
fetches. A closed PR with no `<name>@origin` returns success, so a retry is
safe. `packages/compass-agent/src/cli.ts` adds the tools to `nativeTools`.
`createForgeTools` in `packages/compass-agent/src/forge.ts` drops
`forge_create_pull_request` (D1), and the name list in `forge.test.ts`
changes first. Its comment and review tools send
`broker.idempotencyKey(toolCallId)` as `clientRequestId`, as the create
tools do (D2). Red → green: each D4 refusal fails before the broker call;
the no-issue refusal names `forge_list_issues` and `forge_create_issue`; a
hook failure refuses; a test hook receives `origin`, the URL, and one stdin
line per own bookmark; a hook past 120 s is stopped, the push runs, and the
result carries the fix instruction; the bundle holds every own commit with
its own ID; a call whose bundle plus large entry bodies passes 15 MiB
refuses with no broker call, and one just under passes the gateway;
`vcs_retire` changes no local state when `retire_head` fails.

### T8 — prompt, skill, cutover, and DL-558

`config/prompts/manager/SYSTEM.md` and `config/skills/jj/SKILL.md` name the
tools and drop `jj-vine submit` and `jj-hp`. They say every PR needs an
issue: find one that fits, or create one when none does. They also say a
pre-push timeout means the agent fixes the hook. Red → green:
`packages/compass-agent/src/vcs-prompt.test.ts` fails on `jj-vine submit`.
The Setup agent's prompt installs a basic pre-push hook (format and lint)
in a repo that has none. Cutover (OQ-5): for each open jj-vine PR, the
owning agent resubmits its line through `vcs_submit` with new bookmark
names; the new body links the old PR. The agent then closes the old PR
with `gh pr close --comment <new PR URL>`, under the write token it holds
until T9. `vcs_retire` stays author-only. The cutover is done when no open
PR carries a jj-vine stack block.

### T9 — remove jj-vine and narrow the token

Runs after the T8 cutover. Drop `jjVine` and `[jj-vine]` from
`agent-image/toolchain.nix` (`TestAgentImageVCSTools` in
`go/e2e/image_toolchain_test.go` expects it absent). Set
`gitCredentialPermissions` to `contents: read`, `pull_requests: read`,
pinned in `git_credential_test.go` first. The agent egress allowlist keeps
`github.com` and `api.github.com` for fetch and `gh` reads; the read-only
token is what blocks writes. Files DL-559 and DL-561.

### T10 — Server mirror and push

Interfaces (`go/internal/forge/mirror.go`):

```go
type Mirror interface {
	ApplyBundle(ctx context.Context, repo string, bundle []byte) error
	Push(ctx context.Context, repo string, refs map[string]string) error // ref -> commit; "" deletes
	Rebase(ctx context.Context, repo, ref, onto string) (newHead string, conflict bool, err error)
}
```

A per-repo bare git repo under the Server's `--vcs-mirror-dir`, fetched from the
forge before each apply and pushed with the App token. `Push` pushes only
the refs it is given, with a lease on the old commit. `Rebase` uses
`git replay`. The Server image gains `git`. The mirror is off unless the
`--vcs-mirror-dir` flag is set, so this change adds no spend by itself. Two
caps refuse an apply that would cross them: a per-repo size and a total
size across all mirrors, both flags, with the total below the volume size.
The mirror directory must sit on a fixed-size volume, so disk use cannot
grow the bill. Before any deployment sets `--vcs-mirror-dir`, the change
that enables it must add an enforced cap at each billing layer (account,
cluster, and volume, citing an existing cap where one covers it), a budget
alert that pages Matt through the billing paging route, and a spend-cap row
per layer with evidence that it enforces. Until then the mirror stays off.
Red → green (temp repos): a bundle with missing commits refuses;
a stale lease refuses; a conflicting rebase returns `conflict` and pushes
nothing; an apply over either cap refuses.

## Tasks

- [ ] T1 — provider update and lookup
- [ ] T2 — stack block
- [ ] T3 — proto
- [ ] T4 — store: write intents and case-folded lookup
- [ ] T5 — Server arms and write intents
- [ ] T6 — jj runner and local tools
- [ ] T7 — submit, retire, PR status, and wiring
- [ ] T8 — prompt, skill, cutover, and DL-558
- [ ] T9 — remove jj-vine and narrow the token
- [ ] T10 — Server mirror and push

## Future direction

Out of scope; tracked in RIG-5665. Agents need not stay on git inside
Compass. The internal VCS can follow
[Scion](https://www.anthropic.com/engineering/redesigning-source-control)
(server-side commits and rebases, no push),
[Twigg](https://github.com/twigg-vc/monorepo) (stacked commits, git
mirror), and Piper as closely as possible. GitHub mirroring stays, because
users stay on GitHub. The T10 mirror is the first step.

## Decisions

- **D1** (DL-535) — Local jj in the agent; push and PR writes in the
  Server from a bundle; seven Manager tools beside the `forge_*` tools.
- **D2** (DL-536) — A write intent before every Server create, so no
  created object lacks an owner; any-state head lookup; author-only
  updates and closes; Server-managed same-owner foreign PRs in a line.
- **D3** (DL-537) — An issue per PR in the user's formats, checked open at
  create; commits pushed as made; text composed once.
- **D4** (DL-538) — Linear lines, sync, the repo's own pre-push hook with a
  120 s fail-open limit, and one Server-owned stack block.
