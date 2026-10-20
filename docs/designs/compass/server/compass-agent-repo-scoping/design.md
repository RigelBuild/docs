# Agent repository scoping: spawn-time subset and descendant grants

## Open Questions

**OQ-1 — Which repositories does an agent hold?** This one is load-bearing.
A and C let an agent give only repositories it holds. DL-628 and DL-629 leave
"held" to this question. The Plan assumes (a) until Matt answers: an agent's
own agent rows, as `ListAgentForgeScopeRepos` in
`go/internal/store/forge_scopes.go` reads them: "returns an account's own
rows, not its owner's grants". Two facts limit that rule:

- The root supervisor has no parent, so nothing copies rows into it:
  `// Empty ParentAgentID creates a root.` in `go/server/serve_seed.go`. It
  holds 0 rows, so today it can use neither A nor C.
- Reach always includes the owner's user grants. `HasForgeScope` admits
  `scope.account_id IN ($1, agent.owner_user_id)` and `scope.repo IN ($4,
  '*')`. A and C scope a lane only for repositories that exist as agent rows.

Options:

- **(a) Own rows only; the owner seeds the root.** The owner runs `compass
  agent repo add supervisor <org/name>` once for each repository the tree
  works on, and A and C hand them down from there. The docs state this as the
  operating model. No extra code. Cost: one human step per repository, at the
  root only.
- **(b) Own rows plus the owner's exact user grants, never `*`.** The root can
  hand owner-granted repositories down as rows with no human step. Reach does
  not grow, because the owner grant already covers every agent. But those
  rows narrow nothing; they only mark lanes.
- **(c) A root agent holds any repository its owner's App installation
  covers.** No human step. But a root could give children repositories the
  owner never granted. That widens reach, against DL-458 OQ-2 ("that doesn't
  widen").

**Recommendation:** (a). It keeps one rule, adds no code, and leaves reach
under the owner's control. The Plan assumes (a) until Matt answers. Under (b),
`ListAgentHeldRepos` also returns the owner's exact user rows, and
`copyHeldRepositories` writes child rows for them. Under (c), a root caller
also passes when the repository's org has an App installation. The lifecycle
service then needs the repository service's installation resolver.

**OQ-2 — May an agent also remove a repository from a descendant?**
Independent of OQ-1. Matt's ruling names a grant tool only. Rows carry no
writer, so an agent's remove would also delete rows the owner wrote.

- **A — Grant only, as ruled.** A parent narrows at spawn (A) and widens later
  (C). Narrowing a live child stays with the owner: `compass agent repo
  remove`.
- **B — Add `agents_revoke_repository`.** Same caller, target and held rules
  as the grant. It deletes with `RevokeAgentForgeScope` and refreshes the
  descendant. It needs one more oneof arm and one more tool.

**Recommendation:** A. It ships what was ruled. A parent that spawns narrowly
seldom needs to take a repository back. If Matt picks B, C1, C3 and C4 each
gain the revoke twin of the grant arm, method and tool.

## Problem / Intent

Tracker: RIG-5942. Ruling: Matt, 2026-10-19, on RIG-5942: "B now (keep full
inheritance as shipped). Then build A (a spawn-time subset of the parent's
repos) and C (an `agents_grant_repository` tool limited to descendants). This
keeps agents in their lanes; it is not a security boundary."

A spawned child copies every repository row its parent holds.
`Store.CreateAgent` in `go/internal/store/accounts.go` runs the copy in its
transaction:

```go
if a.ParentAgentID != "" {
	if err := qtx.CopyAgentForgeScopes(ctx, db.CopyAgentForgeScopesParams{
```

So a parent cannot keep a child in its lane. A repository the parent gets
later reaches an existing child only through `compass agent repo add`. This
record lets a parent give a child only its lane's repositories, and push more
down later with no human step.

## Approach

### What stays (B)

The [repos-writer record](../compass-agent-workstream-repos-writer/design.md)
keeps all of its rules (DL-458):

- A spawn without a subset copies every parent row, as today.
- The copy is a snapshot. A row added to or removed from the parent later does
  not reach existing children. C is the explicit way to push a row down.
- The copy runs only on a fresh creation. On a taken handle, `SpawnAsAccount`
  in `go/server/lifecycle.go` resumes (`case errors.Is(err,
  store.ErrConflict):`). `ReparentAgent` copies nothing.
- A subset cannot narrow owner grants, because `HasForgeScope` admits them for
  every agent of that owner.

### Held repositories

**Assumption (OQ-1 (a), pending Matt):** an agent holds a repository when it
has its own agent row for it.
Agent rows are only exact GitHub `org/name`. The owner RPC writes them with
`store.ForgeProviderGitHub` and its host, and the full copy ignores provider
and host. So the new store reads match on `repo` alone, like the full copy.
Neither A nor C needs the forge host, the App or the repository service.

No agent tool in `packages/compass-agent/src` calls `ListAgentRepositories`.
So a "not held" error names both the missing repositories and the caller's
held set. That is how the model learns what it may give.

### A: spawn-time subset

Decision: DL-628. `SpawnPeerRequest` gains a presence-aware
`RepositorySubset repositories = 8`, a message that wraps `repeated string
values`. Unset copies every parent row (B). Set copies exactly the listed
repositories the parent holds. A set, empty list copies none.

- **Presence.** A plain proto3 `repeated` field has no presence, so the
  server could not tell `[]` from unset, and `[]` would mean full
  inheritance. The wrapper follows `BoardRefsOp` in
  `proto/compass/v1/comms.proto`: "an empty present list clears them". An
  empty subset is still a subset of the parent's repositories. It gives a
  child that needs no agent rows. Owner grants still reach it.
- **Normalisation.** Each entry goes through `agentRepository` in
  `go/server/agent_repository_service.go`, which accepts "one exact GitHub
  org/name and stores it in lowercase". Duplicates collapse. A bad entry or
  `*` is `INVALID_ARGUMENT` before any account exists.
- **Copy.** `NewAgent` gains `ParentRepositories *[]string`; nil is the full
  copy. This follows `BoardTaskPatch.Refs *[]string` in
  `go/internal/store/agent_boards.go`. In the `CreateAgent` transaction,
  `copyHeldRepositories` reads the parent's held set. Any missing entry fails
  the transaction with `ErrPermissionDenied`, so no account is left and the
  handle stays free. Otherwise it copies only the listed rows. It reads parent
  rows only, so it never widens.
- **Order.** The copy moves after `insertAccountHandle`. A taken handle then
  returns `ErrConflict` before the copy runs, and the spawn resumes. Today the
  copy runs first. For B the move changes nothing.
- **Replay.** `resumeOrReject` returns or restarts the existing agent and does
  not re-apply the subset. The tool description says so: "repositories apply
  only when the handle is new."
- **No signal.** A new child has no session. Its first credential fetch reads
  its rows.

**DL-090 and DL-442 OQ-8.** DL-090 says "neither SpawnPeerRequest nor
ProvisionAgentWorkspaceRequest carries repo/ref". The subset is not a clone
target. `CreateAgent` consumes it, and `provisionAndStart` never forwards it
to the Runner. Agents still self-clone, and the workstream repository stays a
server-side record. Matt's ruling lifts the repos-writer record's "No spawn
proto change" constraint for this one field.

### C: grant to a descendant

Decision: DL-629. The agent tool `agents_grant_repository` gives some of the
caller's held repositories to a descendant in one all-or-nothing call. It
takes a list, like A, so "spawn a lane, then add three" is one call and one
refresh. It rides `AgentGateway.Lifecycle` as a third oneof arm.
`Gateway.Lifecycle` in `go/internal/runner/gateway/lifecycle.go` forwards a
whole `LifecycleCallRequest`, so the Runner does not change.
`Hub.RelayLifecycleCall` resolves the caller from the session.

The server checks, in this order:

1. `agent_handle` is set, `repositories` is not empty, and each entry passes
   `agentRepository`.
2. The caller's owner resolves (`AgentOwner`), as in spawn.
3. The target resolves through the despawn resolver, renamed
   `resolvePeerTarget`. "Every miss is the one errPeerNotFound."
4. `Store.GiveAgentRepositories` runs one tenant transaction. It takes the
   owner-tree lock, checks that the target is in the caller's subtree (so
   grandchildren count), checks that the caller holds every repository, and
   copies the rows, all or nothing.

**Tree race.** The descendant check and the write must see one tree.
Otherwise a concurrent `ReparentAgent` can move the target out of the
caller's subtree between the check and the write, and the grant lands on a
non-descendant. `ReparentAgent` in `go/internal/store/accounts.go` already
serialises tree changes per owner:

```go
if err := qtx.AcquireOwnerTreeLock(ctx, lockKey); err != nil {
```

`AcquireOwnerTreeLock` is `SELECT pg_advisory_xact_lock(hashtext($1))`. Its
key is the owner id, and it releases at transaction end. The grant takes the
same lock first. It then runs the `AgentSubtree` query and the copy in the
same transaction. A reparent then runs wholly before or wholly after the
grant. A same-owner target shares the caller's lock key, so every reparent
that could move it waits.

There is no self check. `AgentSubtree` includes the caller, and a copy onto
itself conflicts, so nothing is added. There is no `checkOrg`. The row is
held, so the owner RPC checked its org when it wrote the root row. A's copy
skips the check too.

**How the child learns.** When rows are added, the server sends one targeted
signal, `SignalSecretsVersionFor(ctx, target)`. It does not call the broker's
`grantsChanged`, which ends with `b.signal(ctx)`. `buildGitCredentialBroker`
in `go/server/git_credential.go` wires that to the fleet push:

```go
signal: hub.SignalSecretsVersion, clock: time.Now,
```

The repos-writer record rejected a fleet push for agent rows, because every
session in every tenant would re-fetch. An agent-driven C grant would make
that cost grow with agent activity. The broker step is also not needed here.
`grantsChanged` drops served keys that a narrowed set no longer covers, and it
keeps every key with `gitCredentialScopeWithin(key, outer)`. A grant only
widens, so it drops nothing. The child's re-fetch runs `credentials`, which
re-reads `ListForgeScopeRepos` and mints the wider key. A child with no live
session reads its rows at its next start.

**DL-458.** DL-458 ruled "agents never write rows" (OQ-2). Its OQ-1 rejected
"an agent-gateway tool (agents would change credentials)". DL-629 amends both
for this one case, by Matt's ruling. Reach still starts with the owner. Under
OQ-1 (a), every agent row was either written by the owner, or copied or
granted from a row an ancestor held at the time. A granted row stays when the
giver later loses its own row, which is the same snapshot rule as the copy.
DL-075 does not change.

### Errors

| Case | A (spawn) | C (grant) |
| --- | --- | --- |
| Malformed repository, or `*` | `INVALID_ARGUMENT` | `INVALID_ARGUMENT` |
| `repositories` unset | full copy | n/a |
| `repositories` set and empty | no rows | `INVALID_ARGUMENT` |
| A repository the caller does not hold | `PERMISSION_DENIED` | `PERMISSION_DENIED` |
| Unknown, non-agent, other-owner or other-tenant target | n/a | `NOT_FOUND` |
| Same-owner target outside the caller's subtree | n/a | `PERMISSION_DENIED` |

A missing held repository is `PERMISSION_DENIED`, not `INVALID_ARGUMENT`. The
request is well formed, but the caller lacks the authority. The same request
succeeds once the owner adds the row. A target outside the subtree is
`PERMISSION_DENIED`, not `NOT_FOUND`, because `compass_tree` already shows
"every agent your owner owns". All codes reach the agent in-band as a
`LifecycleCallError`.

## Alternatives considered

- **Reject an explicit empty list.** Rejected: a plain `repeated` field has
  no presence, so only the TS edge could reject `[]`. Any other caller would
  still get full inheritance.
- **One repository per C call.** Rejected: one call and one refresh per
  repository, and a different shape from A.
- **The broker's `grantsChanged` for C.** Rejected: it pushes to the whole
  fleet, as shown above.
- **Wiring the repository service into the lifecycle service.** Rejected: the
  reads match on `repo` alone, and the org was checked when the root row was
  written.
- **A new AgentGateway RPC for C.** Rejected: the lifecycle family already has
  the broker, relay, in-band errors and Runner forward.
- **Live inheritance of later parent rows.** Rejected: it reverses DL-458
  OQ-3. C covers the push-down case on purpose.

## Global Constraints

- **Public repo.** Never name private repositories, hosts or deployments.
  Tracker ids are bare text (`docs/designs/compass/CONTRIBUTING.md`).
- **Proportionate checks.** This keeps agents in lanes; it is not a security
  boundary. Add no writer column, audit table, rate limit or approval gate.
- **Two PRs, each live when it lands.** PR 1 is A end to end (A1 to A4). PR 2
  is C end to end (C1 to C4). PR 2 stacks on PR 1 because both edit
  `agent_gateway.proto`, `forge_scopes.sql`, `lifecycle.go` and
  `lifecycle.ts`.
- **Field numbers.** `SpawnPeerRequest.repositories = 8`, a `RepositorySubset`.
  Field 7 stays free for the planned `profile = 7` in the per-agent overrides
  record (DL-289).
  `LifecycleCallRequest.grant_repository = 4`;
  `LifecycleCallResult.grant_repository = 5`.
- **No public surface.** `proto/compass/v1/compass.proto`, the CLI and
  `classifyProcedure` do not change. AgentGateway is internal.
- **Exact repositories.** Every entry goes through `agentRepository`. Rows stay
  lowercase `org/name`, never `*`.
- **Oracle-safe misses.** C's unknown, non-agent, other-owner and other-tenant
  targets all return the one `errPeerNotFound`.
- **No fleet signal.** Neither path calls `grantsChanged` or
  `SignalSecretsVersion`.
- **Generated code.** Run the proto `gen` task and the Go `sqlc-gen` task.
  Never hand-edit `go/gen`, `go/internal/gen`, `go/internal/store/db` or the TS
  `gen` trees.
- **Store errors.** Use `fmt.Errorf("%w: ...", ErrInvalidArgument)` and
  `ErrPermissionDenied`. No migration.
- **Tests.** Pgtests use `pgtest.RequireDSN`. Tenant cases use `seedTenant` and
  `WithTenant`. Each rejection test also runs the healthy case, which must
  pass.

## Plan

**Assumption.** The Plan implements OQ-1 (a) until Matt answers. If he picks
(b) or (c), apply the delta that OQ-1 names. DL-628 and DL-629 hold either
way.

### PR 1 — A: spawn-time subset

#### A1 — Proto

In `proto/compass/v1/agent_gateway.proto`:

```proto
// A spawn-time repository subset (DL-628). Present means exactly these
// parent rows; an empty present list copies none.
message RepositorySubset {
  repeated string values = 1;
}
```

`SpawnPeerRequest` gains:

```proto
// Grant scoping, never a clone target (DL-090). Unset copies every parent
// row. Applies only when the handle is new.
RepositorySubset repositories = 8;
```

Its comment "never a repo/ref" becomes "never a clone target". Regenerate.
Export `RepositorySubsetSchema` from `packages/compass-agent/src/compassv1.ts`.

Interfaces: produces `SpawnPeerRequest.GetRepositories() *RepositorySubset`
(nil when unset) and TS `repositories?: RepositorySubset`. Tests: proto
`lint` and `drift`.

#### A2 — Store

In `go/internal/store/queries/forge_scopes.sql`:

```sql
-- name: ListAgentHeldRepos :many
-- An agent's own rows on any host; agent rows are exact GitHub org/name.
SELECT DISTINCT scope.repo
FROM account_forge_scopes AS scope
JOIN agent_accounts AS a ON a.account_id = scope.account_id
WHERE a.account_id = $1
ORDER BY scope.repo;

-- name: CopyAgentForgeScopeSubset :many
-- CopyAgentForgeScopes limited to the named repositories.
INSERT INTO account_forge_scopes (account_id, forge_provider, forge_host, repo)
SELECT child.account_id, scope.forge_provider, scope.forge_host, scope.repo
FROM account_forge_scopes AS scope
JOIN agent_accounts AS parent ON parent.account_id = scope.account_id
JOIN agent_accounts AS child ON child.account_id = sqlc.arg(child_id)
WHERE parent.account_id = sqlc.arg(parent_id)
  AND parent.owner_user_id = child.owner_user_id
  AND scope.repo = ANY(sqlc.arg(repos)::text[])
ON CONFLICT DO NOTHING
RETURNING repo;
```

In `go/internal/store/forge_scopes.go`:

```go
// copyHeldRepositories copies from's rows for repos to to, all or nothing. It
// lowercases and dedupes repos; empty repos copies nothing. A repository from
// does not hold is ErrPermissionDenied naming the missing and held sets.
// added lists the rows to did not already hold.
func copyHeldRepositories(ctx context.Context, q *db.Queries, from, to AccountID, repos []string) (added []string, err error)
```

The error text is `not held: <missing>; held: <held or "none">`.
`NewAgent` in `go/internal/store/inputs.go` gains `ParentRepositories
*[]string // nil copies every parent row; non-nil copies only these, possibly
none`. In `Store.CreateAgent`, a non-nil subset without a parent is
`ErrInvalidArgument`. The copy moves after `insertAccountHandle`. It is
`copyHeldRepositories` when the field is non-nil, and `CopyAgentForgeScopes`
when it is nil.

Interfaces: produces `NewAgent.ParentRepositories` for A3 and
`copyHeldRepositories` for C2. Pgtests in
`go/internal/store/forge_scopes_pgtest_test.go`:

- A subset copies only the listed parent rows. Mixed case and duplicates
  normalise.
- A non-nil empty subset copies no rows. A nil subset copies every row.
- A repository the parent lacks, or reaches only through an owner grant, is
  `ErrPermissionDenied`, and the text names the missing and held sets. No
  account or row remains, and a held retry succeeds.
- A non-nil subset without a parent is `ErrInvalidArgument`.
- A taken handle with an unheld subset is `ErrConflict`.
- A tenant-B parent copies its subset under `WithTenant`. Tenant A sees no
  child.

#### A3 — Server

In `go/server/agent_repository_service.go`:

```go
// agentRepositoryList normalises a list with agentRepository and dedupes it.
// It returns a non-nil slice, so an empty input stays an empty subset.
func agentRepositoryList(raw []string) ([]string, error)
```

`SpawnAsAccount` reads `req.GetRepositories()` after `CheckClientRequestID`.
When the subset is set, it passes `&list` as `NewAgent.ParentRepositories`,
where `list` is `agentRepositoryList(subset.GetValues())`. A bad entry is
`INVALID_ARGUMENT`. The `CreateAgent` switch maps `ErrPermissionDenied` to
`PERMISSION_DENIED` and `ErrInvalidArgument` to `INVALID_ARGUMENT`, both with
the store text.

Interfaces: consumes A1 and A2. Produces `agentRepositoryList` for C3.
Pgtests in `go/server/agent_repository_pgtest_test.go`, using
`newAgentRepositoryLifecycleTest`:

- The parent holds `owner/a` and `owner/b`. A spawn with `["OWNER/A"]` leaves
  rows `[owner/a]`, and the child's mint covers `a` and `base`.
- A spawn with a set, empty subset leaves no rows, and the child's mint covers
  only `base`. A spawn with the subset unset copies every row.
- An unheld repository is `PERMISSION_DENIED` naming it and the held set. The
  handle stays free, and a held retry succeeds.
- `*` and `a/b/c` are `INVALID_ARGUMENT`.
- A same-handle replay with another subset returns the same id and rows.

#### A4 — Agent tool and docs

In `packages/compass-agent/src/lifecycle.ts`, `spawnParameters` gains
`"repositories?": type("string[]")`. Its description says: omit it to give
every repository you hold; list exact `org/name` entries you hold to give only
those; an empty list gives none; repositories apply only when the handle is
new; your owner's grants reach every agent. The spawn tool sets the field only
when the parameter is present:

```ts
repositories:
	params.repositories === undefined
		? undefined
		: create(RepositorySubsetSchema, { values: params.repositories }),
```

Interfaces: consumes A1. Tests in `lifecycle.test.ts`: the exact
`SpawnPeerRequest` when the parameter is omitted (field unset), when it is
`[]` (field set, no values) and when it is `["owner/a"]`.

Docs: `docs/self-host.md` (Agent workstream repositories) states the
operating model: the owner adds the tree's repositories to the root
supervisor, and a parent narrows each child at spawn. Also update
`packages/compass-agent/AGENTS.md` (the lifecycle toolset) and
`config/skills/management-trees/SKILL.md` (Subagents vs standing Managers).

### PR 2 — C: descendant grant

#### C1 — Proto

```proto
// Give some of the caller's held repositories to a descendant (DL-629).
message GrantDescendantRepositoriesRequest {
  string agent_handle = 1;          // a descendant: bare or `owner/agent`
  repeated string repositories = 2; // exact `org/name`; all or nothing
}
message GrantDescendantRepositoriesResponse {
  repeated string added = 1; // rows the descendant did not already hold
}
```

Add `GrantDescendantRepositoriesRequest grant_repository = 4;` to the
`LifecycleCallRequest` oneof, and `GrantDescendantRepositoriesResponse
grant_repository = 5;` to the `LifecycleCallResult` oneof. Regenerate. Export
both schemas and types from `packages/compass-agent/src/compassv1.ts`.

Interfaces: produces `LifecycleCallRequest_GrantRepository`,
`LifecycleCallResult_GrantRepository` and the TS case `"grantRepository"`.
Tests: proto `lint` and `drift`.

#### C2 — Store

```go
// GiveAgentRepositories copies from's rows for repos to to, all or nothing,
// in one tenant transaction under the owner-tree lock. to must be in from's
// subtree with the same owner, else ErrPermissionDenied. Empty repos is
// ErrInvalidArgument.
func (s *Store) GiveAgentRepositories(ctx context.Context, from, to AccountID, repos []string) (added []string, err error)
```

On `s.q.WithTx(tx)` after `s.beginTenantTx`, it runs in order:
`GetAgentOwner(from)`, `AcquireOwnerTreeLock(owner)`, the `AgentSubtree`
query for `from`, then `copyHeldRepositories`. An unknown `from` is
`ErrNotFound`.

Interfaces: consumes A2. Produces the method for C3. Pgtests in
`go/internal/store/forge_scopes_pgtest_test.go`:

- A give to a grandchild adds the rows. A repeat adds nothing.
- A target outside `from`'s subtree, and a target with another owner, are
  `ErrPermissionDenied` and write nothing.
- A list with one unheld entry is `ErrPermissionDenied` and writes nothing.
- An empty list is `ErrInvalidArgument`.
- Under tenant A, a tenant-B `from` is `ErrNotFound`.
- `TestGiveAgentRepositoriesSerializesWithReparent` follows
  `TestReparentAgentConcurrentCycleSerialized` in
  `go/internal/store/agent_tree_test.go`. tx1 takes the owner's advisory
  lock and moves the target out from under the giver, without committing. A
  goroutine calls `GiveAgentRepositories`. The test polls `pg_locks` until the
  grant waits on that key, then commits tx1. The grant is
  `ErrPermissionDenied`, and the target has no new row. In the control run,
  tx1 moves the target under the giver instead, and the grant succeeds.
  Without the lock, the grant reads the old tree and writes, so the test
  fails.

#### C3 — Server

In `go/internal/runnerhub/relay_lifecycle.go`, `LifecycleCaller` gains:

```go
GrantRepositoriesAsAccount(ctx context.Context, caller store.AccountID, req *compassv1internal.GrantDescendantRepositoriesRequest) (*compassv1internal.GrantDescendantRepositoriesResponse, error)
```

`executeLifecycleCall` gains the `grant_repository` case. Its unset-oneof
message becomes "lifecycle call has no variant set". `fakeLifecycleCaller` in
`go/internal/runnerhub/helpers_test.go` gains the method.

In `go/server/lifecycle.go`, `lifecycleService` gains `signal
agentSecretsSignaler`. `newLifecycleService` sets it to the hub, as a nil
interface when the hub is nil. Rename `resolveDespawnTarget` to
`resolvePeerTarget`. `GrantRepositoriesAsAccount` runs checks 1 to 3 of the C
section, then calls `GiveAgentRepositories(caller, target, repos)`. It maps
`ErrPermissionDenied` and `ErrInvalidArgument` to their codes, with the store
text, and any other error to `INTERNAL`. When `added` is not empty, it calls
`signal.SignalSecretsVersionFor` for the target. A signal failure is logged,
never returned.

Interfaces: consumes C1, C2 and `agentRepositoryList`. Produces the relay arm
for C4. Tests:

- `relay_lifecycle_test.go`: the arm reaches the caller with the resolved
  account and returns `grant_repository`. A caller error renders in-band with
  its code.
- `TestGrantDescendantRepositories` (pgtest). The test broker's `signal` counts
  fleet pushes, and `lc.signal` is a `recordingAgentRepoSignaler`. The owner
  adds `owner/a` and `owner/b` to the parent. The parent spawns a child with
  `[owner/a]`, and the child spawns a grandchild.
  - The parent grants `["OWNER/B"]` to the child. Added is `[owner/b]`, rows
    are `[owner/a, owner/b]`, the targeted signals are `[child]` and the fleet
    count is 0. The child's next mint covers `a`, `b` and `base`.
  - A repeat adds nothing and sends no signal. A grant to the grandchild
    succeeds.
  - The child granting to the parent or to a sibling is `PERMISSION_DENIED`.
  - `[owner/a, owner/c]` is `PERMISSION_DENIED` naming `owner/c`, and the
    target's rows do not change. `owner/base` alone (an owner grant) is
    `PERMISSION_DENIED`.
  - An unknown handle, and another owner's agent bare or qualified, are
    `NOT_FOUND`. `*` and an empty list are `INVALID_ARGUMENT`.

#### C4 — Agent tool and docs

In `packages/compass-agent/src/lifecycle.ts`:

```ts
export const grantRepositoryParameters = type({
	agent_handle: type("string")
		.narrow((s, ctx) => s.trim().length > 0 || ctx.mustBe("non-blank"))
		.describe("A descendant's agent handle; must not be blank"),
	repositories: type("string[]")
		.narrow((a, ctx) => a.length > 0 || ctx.mustBe("non-empty"))
		.describe("Exact org/name repositories you hold; all or nothing"),
});
```

`agents_grant_repository` has `approval: "write"` and sends `case:
"grantRepository"`. It renders `Granted <added> to <handle>.` or `<handle>
already holds every listed repository.`, with each value passed through
`attr`. Failures go through `lifecycleFailure`. `createLifecycleTools` returns
three tools.

Interfaces: consumes C1 and the C3 arm. Tests:

- `lifecycle.test.ts`: "exposes exactly the two lifecycle tools" becomes
  three, each `write`. Test the exact grant request, both renderings and the
  in-band error.
- `cli.test.ts`: both `toHaveLength(29)` become 30.

Docs: `docs/concepts/tools.md` (Lifecycle), `packages/compass-agent/AGENTS.md`,
`config/skills/management-trees/SKILL.md` and `docs/self-host.md` gain the
grant tool: push a repository down to a descendant without the owner.

## Tasks

- [ ] A1: `RepositorySubset` and `SpawnPeerRequest.repositories = 8`, regenerated and exported.
- [ ] A2: `ListAgentHeldRepos`, `CopyAgentForgeScopeSubset`, `copyHeldRepositories` and the `CreateAgent` subset, with pgtests for set, empty and unset subsets.
- [ ] A3: `agentRepositoryList` and the spawn subset in `SpawnAsAccount`, with pgtests.
- [ ] A4: the `repositories` spawn parameter, its TS tests and the root-seeding docs.
- [ ] C1: the `grant_repository` arms, regenerated and exported.
- [ ] C2: `Store.GiveAgentRepositories` under the owner-tree lock, with pgtests and the reparent interleave test.
- [ ] C3: `GrantRepositoriesAsAccount`, the relay arm and the targeted signal, with relay tests and pgtests.
- [ ] C4: `agents_grant_repository`, its TS tests and the doc updates.
