# Linear board ingest

Tracker: RIG-5949. Lane: compass-server. Ledger: DL-630.

## Open Questions

The record is designed against each recommendation.

1. **Tracker resolve gate (depends on RIG-5100).** DL-467 makes the lane's
   sink call `tracker.ResolveLinearStatus`. That package is not on main. The
   RIG-5077 record defines it in its T1, and that record stays a draft until
   RIG-5100 is ruled. Three of those questions shape T1: RIG-5100 OQ-1 (echo
   suppression), OQ-4 (unknown type: no move or BACKLOG) and OQ-5 (rule key:
   state name or state id). Until a resolve runs, every Linear row keeps the
   DDL default `state = 1` (BACKLOG). BACKLOG is not in `BOARD_LANES`
   (`apps/ui/src/constants.ts`), and `fleetMetrics`
   (`apps/ui/src/components/RightSidebar.tsx`) does not count it. So T1–T4
   fill `issues`, but the board grid and the Status counts stay empty until
   T5. After T5, the type defaults put only `started` and closed issues on
   the grid; `unstarted` adds only to the `todo` count.
   - (a) T5 waits for RIG-5077 T1 and T3.
   - (b) T5 waits for RIG-5077 T1 only and resolves with
     `store.DefaultTrackerConfig()`. This still waits on the whole RIG-5100
     ruling.
   - (c) T5 maps Linear state types inline now. This contradicts DL-467 ("this
     record defines no mapping").
   - (d) Matt rules only RIG-5100 OQ-1, OQ-4 and OQ-5 now. RIG-5077 T1, a
     pure function, then freezes, and this lane lands it as T0. T5 resolves
     with `store.DefaultTrackerConfig()` until RIG-5077 T3 adds
     `CurrentTrackerConfig`.

   **Recommendation: (d).** It is the fastest real fix that keeps DL-467. The
   board stays empty until those three questions are ruled.
2. **Cold-start scope.** A zero watermark walks the team's issues once.
   - (a) Full history, as the GitHub walk does (DL-281).
   - (b) Open issues of any age, plus issues updated in the last 30 days (the
     window DL-412 uses for PRs). Open includes `backlog` and `triage` of any
     age, so a large team can pass the bound below.
   - (c) `started` and `unstarted` issues of any age, plus any issue updated
     in the last 30 days. Stale `backlog` and `triage` stay out.

   **Recommendation: (c).** `ListBoardIssues` returns the whole projection in
   one response, and its proto comment bounds it at "low thousands". (a)
   fills the Done column with years of closed issues; stale backlog adds
   nothing to the grid. Cost of (b) and (c): a `KEY-N` ref (DL-450) to an
   older issue has no row until the issue is next updated. Nothing on main
   auto-archives, so the Done column still grows with every later close.
3. **Assignee.** `store.Issue` says "assignee = Dispatcher", and the UI reads
   `Issue.assignee` as a Compass account id (`adapt.ts`, `agentView` in
   `Bridge.tsx`).
   - (a) Do not map the Linear assignee. The field stays empty.
   - (b) Write the Linear display name. The UI would treat a name as an
     account id.
   - (c) Map Linear users to Compass accounts. No such link exists.

   **Recommendation: (a).** Revisit when an account link exists.
4. **A last-resolved state id (amends DL-467).** DL-467 says the sink "stores
   tracker_state_id and tracker_status". One column cannot hold both the last
   observed and the last resolved state. Rows ingested by T3–T4 already hold
   their current id, so T5 would never resolve them. A resolve that fails
   after the upsert commits is lost. The arm and the sweep run in separate
   goroutines, so two observations can land the older target last.
   - (a) Add `tracker_resolved_state_id`, written only after a resolve
     commits or is a deliberate no-op. A resolve runs while it differs from
     `tracker_state_id`, re-checked under `transitionMu` against the current
     row. A boot pass resolves every row where the two differ.
   - (b) T5 re-resolves every provider-4 row once. This fixes only the
     pre-T5 rows.
   - (c) T3 does not store `tracker_state_id`. Old rows still need (b) or a
     watermark reset.

   **Recommendation: (a).** The record is designed on it (D5, T5). Decision
   needed: Matt confirms this amendment to DL-467.
5. **Deleted and team-moved issues.** `ParseLinearDataEvent` drops `remove`.
   A team move sends `update` under the new key only, and Linear's `issues`
   query hides archived and trashed issues. So the old row keeps its last
   state; after T5 an open one keeps counting in `active` or `todo`. A
   team-key rename strands the whole team, because its subscription keys on
   the old key.
   - (a) Accept the drift for v1 and state it (D5).
   - (b) Store the Linear issue UUID (`linearIssue.ID`, which `toIssue` drops
     today). When a known UUID arrives under a new coordinate, archive the
     old row with `SourceAuto`.
   - (c) Archive a row when its hydrate returns 404. The arm must then admit
     `remove`, and this handles deletes only.

   **Recommendation: (a)** now, with a later issue to track (b).

## Problem / Intent

Matt, 2026-10-19: the Bridge board is empty and the Status pane counts read 0.
Both read the server's `issues` table. The GitHub board lane fills it, but
Linear has no board lane, so no RIG issue reaches the board. Matt ruled option
A on RIG-5949: build a Linear board lane on the same `Ingester` the GitHub lane
uses, fed by a Linear list call in the reconciler and by Linear `Issue`
webhooks, both writing through `IssueProjection.PublishIssueUpdate`. The
`issues` table stays a projection of forge issues. Seeding the GitHub repo
subscriptions of a deployment is out of scope.

## Approach

DL-467 (Active) already rules that Linear issues become `issues` rows with
provider 4 through a Linear board lane. Task T12 of the agent status record
sketches it. This record executes T12 against current main and the 10-19
ruling, and fixes the gaps found since.

### D1 One Ingester, two feeds

Ledger: DL-630.

For Linear, `issues` is a projection of the subscribed teams' issues, scoped
by the cold-start and drift policy in OQ-2 and OQ-5. It is not a full copy of
each team.

The lane is the GitHub board lane with a Linear client. No new ingest type:

| Part | GitHub lane today | Linear lane |
| -- | -- | -- |
| `ingest.Ingester` | over `*forge.OwnerRoutedGitHub` | over `*forge.Linear` |
| `ingest.BoardWebhookArm` | fed by `/webhooks/github` | fed by `/webhooks/linear` |
| `ingest.BoardReconciler` | `Pulls` set | `Pulls: nil` |
| Store adapters | `(GITHUB, host)` | `(LINEAR, "linear.app")` |
| Sink | `IssueProjection` | `IssueProjection` (T5: `linearBoardSink`) |

`*forge.Linear` already has `ListIssues` (the `forgeReader` seam) and
`GetIssueConditional` (the `issueHydrator` seam). T1 adds
`ListUpdatedIssues` (the `updatedLister` seam). Linear has no PRs (DL-203),
so `Pulls` stays nil. `boardRelevant` in `go/internal/ingest/board_webhook.go`
admits a PR event only when the arm can hydrate PRs, so the Linear arm drops
them.

The webhook arm re-reads each issue through `GetIssue`; it never writes the
payload. Both feeds therefore write the same shape, read through one fragment.

### D2 Mapping

| Board field | Linear source |
| -- | -- |
| coordinate | provider 4, host `linear.app`, `repo` = team key (case kept), `number` |
| `title`, `body`, `url`, `labels` | `title`, `description` (owner header stripped), `url`, label names |
| `forge_state` | `mapLinearState`: completed, canceled, duplicate → `closed` |
| `forge_account` | `creator.displayName` (empty for an app) |
| `tracker` | `{kind "linear", id "KEY-N", status, url, state_id, state_type}` |
| `state` | T5 only, through the RIG-5077 resolver (OQ-1, OQ-4) |
| `assignee` | not mapped (OQ-3) |

`issueKey` in `apps/ui/src/board-render.ts` already shows `tracker.id`.
`issues` gains `tracker_state_id`, `tracker_state_type` and `tracker_status`
(DL-467), so `tracker` survives a restart. T5 adds
`tracker_resolved_state_id` (OQ-4).

PR linkage: an explicit `pull_request_issue_links` row may already name a
Linear target (`issue_forge_provider` admits 4). `PublishIssueUpdate` calls
`FallbackIssuesForTarget`, so the PR attaches when the Linear row arrives. That
ends DL-410's fallback "until Linear issues are board rows". DL-450's `KEY-N`
board refs need `board.Forges.LinearTeams`, which `setCommsForges` in
`go/server/serve.go` never sets. T4 fills it.

### D3 Scope: Linear teams in `forge_repo_subscriptions`

Scope mirrors the GitHub lane exactly (DL-162). A Linear team key is a
`forge_repo_subscriptions` row under `(4, "linear.app")`. A new
`--forge-linear-teams` flag (`$COMPASS_FORGE_LINEAR_TEAMS`) seeds it at boot,
like `--forge-repos`, through `EnsureForgeRepoSubscription` (`ON CONFLICT DO
NOTHING`). The table is authoritative after the first insert. A key matches the
`refs` tracker grammar (an upper-case letter, then 1–9 upper-case letters or
digits), so every subscribed team's `KEY-N` refs parse.

A subscription row turns on four things, not only the lane:

- the board sweep and the webhook gate (this record);
- a Linear link rule in `refLinkRuleSource.rules`
  (`go/server/ref_link_rules.go`);
- a mirror-settings row from `ListForgeMirrorSettings`;
- `board.Forges.LinearTeams`, for `KEY-N` board refs (T4).

`refLinkRuleSource.rules` reads the table on every call, but T4 reads
`LinearTeams` once at boot. A team added at runtime gets link rules at once
and board refs after a restart.

The store's enabled-repo reads are repo-only today. `ListEnabledForgeRepos`
selects `WHERE enabled = TRUE`, and `IsEnabledForgeRepo` matches `repo = $1`.
The GitHub sweep would list a Linear team key as a repo. T2 makes the point
check coordinate-scoped. The reconcile adapter switches to the existing
`ListEnabledForgeRepoSubscriptions(provider, host)`, and the repo-only list is
deleted. `normalizeBoardRepo` lowercases every repo; T3 makes it lowercase
GitHub only, the rule of `ForgeCoord.Normalized`.

### D4 Reconciler cadence and Linear rate limits

The reconciler runs as the GitHub one: one sweep at start, then every
`defaultBackstop` (30 min), 200 ms between teams. `ListUpdatedIssues` pages
`issues(filter: {team, updatedAt: {gte: since}}, orderBy: updatedAt,
first: 50)`. Linear has no ETags, so it returns no ETag and the sweep never
304s. The watermark advances after the sink.

Budget. An OAuth app gets 5,000 requests and 2,000,000 complexity points per
hour. A steady sweep costs about one request per team: 2 per team per hour.
A webhook costs one `GetIssue` per coalesced issue. A cold start of N issues
costs N/50 requests once. The board lane reuses the notify lane's client
(T4), so both read lanes share one rate gate. On a 429 or `RATELIMITED`,
`doGraphQL` returns `*forge.RateLimitError`, and the sweep aborts until the
next tick. `BoardWebhookArm.hold` keeps only PR keys, so the Linear arm drops
each issue key and logs one Warn per batch while the gate is shut. The sweep
heals them: their `updatedAt` is above the watermark. The write coordinate
keeps its own client and gate; Linear counts all three against one app quota
anyway.

### D5 Webhook idempotency and ordering

- **Key.** The coordinate is the idempotency key (`issues_coordinate_key`).
  A replayed or duplicate delivery re-upserts the same row.
- **Order.** Both feeds read the issue live, so a late delivery writes the
  newest data. The upsert's recency guard (`forge_updated_at`, DL-129)
  drops a read older than the stored row, so a slow sweep page cannot undo a
  newer webhook read.
- **Coalescing.** `drainBatch` merges queued events per coordinate, so a burst
  on one issue costs one read.
- **Loss.** A full arm queue drops with a warning; the next sweep heals it.
- **Delete and move (OQ-5).** A deleted or team-moved Linear issue keeps its
  old row and last state. After T5 an open one keeps counting in `active` or
  `todo`, and a renamed team key strands every row of the team. v1 accepts
  this drift.
- **State change (T5, OQ-4).** `tracker_state_id` is the last observed state;
  `tracker_resolved_state_id` is the last resolved one. A resolve runs while
  they differ. It reads the current row under `transitionMu`, so it resolves
  the newest observation, then marks that id resolved. A failed resolve stays
  pending until the next observation or the boot pass.

### D6 Tenant scoping

The webhook and the sweep set no tenant, as on GitHub. `Store.resolveTenant`
falls back to the bootstrap tenant, the only tenant production creates. Rows,
subscriptions and watermarks all sit under RLS (`tenant_isolation`). The seed
runs at boot under the same fallback. T5 loads the tracker config under the
same context as the upsert, never `WithSystemRole`. One Linear team in several
tenants is out of scope, as one GitHub repo is today.

### D7 Spend freeze

No new billed resource: no process, pod, table, bucket or vendor. The schema
change is four columns. Linear calls fall inside the existing app's free
quota. No new metric name: the arm shares the `compass_board_webhook_drops`
expvar. No new log line on the success path. The new Warns are one at boot
when teams are enabled with no webhook (T4), and one per arm batch while the
Linear rate gate is shut (D4).

Traces come from the pgx tracer and the two lane root spans:

- Each ingested issue costs about 5 query spans (upsert, `GetIssue`, the PR
  read, `FallbackIssuesForTarget`). T5 adds 2 when the state holds and about
  5 when it moves.
- Each Linear `Issue` event in the workspace costs one `ingest.board.drain`
  span and one gate query, even for a team with no subscription. Example: 500
  events a day cost about 1,000 spans, plus the per-issue cost.
- A cold start is one trace of about 5 × N spans per team, once. OQ-2 bounds
  N.

Each implementation PR carries one line:

```text
Cost-impact: series +0; logs +0 GB/day (Warn on failure only); logs-query +0 GB/day; traces about 5-10 spans per ingested Linear issue
```

## Global Constraints

- **Public repo.** Never name private repositories, hosts, deployments or
  roadmap. Tracker ids are bare text.
- **Lane and VCS.** compass-server; jj; one linear stack T0 → T5 (T0 only
  under OQ-1(d)).
- **Migrations.** T2 adds `0005_linear_board_rows.sql`; T5 adds the next free
  number. Never edit an applied migration: the runtime checksum and
  `checkMigrationImmutability` reject it. Run `sqlc generate` after query
  changes.
- **Coordinate.** Linear is provider 4 (`store.ForgeProviderLinear`), host
  `forge.LinearHost`, repo = team key with its case kept.
- **Package rules.** `go/internal/ingest` imports no store. Store adapters
  live in `go/server`.
- **No PRs on Linear.** `pull_requests` keeps `CHECK (forge_provider IN (1, 2,
  3))`.
- **Spend.** No new metric name, no per-row Info log, no new process or table.
- **Tests.** Unit tests with `scriptedRoundTripper` and golden fixtures under
  `go/internal/forge/testdata/linear/` (DL-210); pgtests with build tag
  `pgtest && unix`; a fake Linear GraphQL server modelled on `fakeLinearAPI`
  in `go/server/linear_webhook_e2e_pgtest_test.go`. Red first.

## Plan

### T0 Tracker resolver (forge, store, tracker; only under OQ-1(d))

Land RIG-5077 T1 as written once RIG-5100 OQ-1, OQ-4 and OQ-5 are ruled. It
is pure: exported Linear type constants, the config types and the resolver.
Its tests are the ones RIG-5077 T1 lists.

Interfaces (from the RIG-5077 record, T1):

```go
// go/internal/forge/linear.go: exported LinearType* constants, plus
var LinearStateTypes []string

// go/internal/store/tracker_config.go
type TrackerStatusRule struct { TeamKey, StatusName string; State IssueState }
type TrackerConfig struct { Rules []TrackerStatusRule }
func DefaultTrackerConfig() TrackerConfig
func ValidateTrackerConfig(cfg TrackerConfig) error

// go/internal/tracker/linear.go
type LinearWorkflowState struct { Name, Type string }
type Resolved struct { State store.IssueState; StatusName string }
func ResolveLinearStatus(cfg store.TrackerConfig, teamKey string, ws LinearWorkflowState) (Resolved, bool)
```

If RIG-5100 OQ-5 rules the state id as the rule key, `LinearWorkflowState`
gains an id, and T5 passes `TrackerStateID`.

### T1 Linear updated-issue walk (forge)

Read the state id, name and type, and list updated issues.

- `issueFieldsFragment` reads `state { id name type }`. `linearIssue.State`
  gains `ID`. `toIssue` fills the three new fields.
- `ListUpdatedIssues` walks pages with the filter in D4, following
  `pageInfo` like `ListIssues`. A zero `since` uses the OQ-2(c) filter:
  `or: [{state: {type: {in: ["started", "unstarted"]}}}, {updatedAt: {gte:
  now-30d}}]`. Rows with a zero `UpdatedAt` are skipped, as on GitHub.
- Any page error fails the whole walk, with no rows. That includes
  `hasNextPage` with an empty cursor: return `forge: linear list updated
  issues %q: next page has no cursor`, as `ListCommentsSince` does.
  `ListIssues` stops silently there instead. A partial walk must never
  reach `sinkRows`, or its max `updatedAt` would advance the watermark past
  the pages it never read. `reconcileRepo` returns a list error before
  `sinkRows`, so the watermark stays put.
- Golden fixtures: refresh `list_issues.json` and `get_issue.json`; add
  `list_updated_issues.json` and `list_updated_issues_cold.json`.

Interfaces:

```go
// go/internal/forge/provider.go — Issue gains (Linear only; empty elsewhere):
StateID, StateName, StateType string

// go/internal/forge/notify_reader.go
const linearColdStartWindow = 30 * 24 * time.Hour
func (l *Linear) ListUpdatedIssues(ctx context.Context, team string, since time.Time, _ string) (ConditionalResult[UpdatedRows], error)
```

Tests: the request filter for a set and a zero `since`; two pages; a malformed
page (`hasNextPage` with an empty cursor) on page 2 returns the error and no
rows; `RATELIMITED` on page 2 returns `ErrBudgetExhausted` and no rows;
`UpdatedRows.Pulls` is always nil. T4 tests the watermark.

### T2 Schema and coordinate-scoped store (store)

```sql
-- 0005_linear_board_rows.sql
-- Linear issues become board rows (DL-467): admit provider 4 and keep the
-- last observed Linear workflow state.
ALTER TABLE issues DROP CONSTRAINT issues_forge_provider_check;
ALTER TABLE issues ADD CONSTRAINT issues_forge_provider_check
    CHECK (forge_provider IN (1, 2, 3, 4));
ALTER TABLE issues
    ADD COLUMN tracker_state_id   TEXT NOT NULL DEFAULT '',
    ADD COLUMN tracker_state_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN tracker_status     TEXT NOT NULL DEFAULT '';
```

- `UpsertIssueForgeFields` writes the three columns in the insert and in the
  guarded `DO UPDATE`. `GetIssue`, `ListIssues`, the search query and
  `IssuesByCoords` (`queries/agent_boards.sql`) select them.
- `IsEnabledForgeRepo` gains `forge_provider = $1 AND forge_host = $2`.
  Delete the `ListEnabledForgeRepos` query and method. Callers to update:
  `forge_cursors_pgtest_test.go`, `trace_pgtest_test.go` (switch to
  `ListEnabledForgeRepoSubscriptions`) and `merge_queue_pgtest_test.go`.
- `boardTargetStore` binds `provider` and `host` like `boardReconcileStore`.
  `boardReconcileStore.ListEnabledRepos` maps
  `ListEnabledForgeRepoSubscriptions(provider, host)` to repo strings.

Interfaces:

```go
// go/internal/store — IssueForgeFields and Issue gain:
TrackerStateID, TrackerStateType, TrackerStatus string
func (s *Store) IsEnabledForgeRepo(ctx context.Context, p ForgeProvider, host, repo string) (bool, error)

// go/server
type boardTargetStore struct { st *store.Store; provider store.ForgeProvider; host string }
```

Tests (pgtest): a provider-4 upsert succeeds and round-trips the columns; the
recency guard still holds; a Linear subscription `RIG` is not enabled for
`(GITHUB, github.com)` and is not in the GitHub list; tenant B cannot see
tenant A's Linear row.

### T3 Tracker field and provider-aware normalize (board, ingest)

- `Ingester.translateOne` sets `issue.Tracker` when its `ForgeRef` is LINEAR:
  `{Kind: "linear", Id: repo + "-" + number, Status: raw.StateName, Url:
  raw.URL, StateId: raw.StateID, StateType: raw.StateType}`.
- `protoToForgeFields` copies `tracker.state_id`, `state_type` and `status`.
  `issueToProto` rebuilds `tracker` from the row for provider 4 only.
- `normalizeBoardRepo` lowercases GitHub only; `drainBatch` passes
  `ev.Provider`.

Interfaces:

```go
// go/internal/ingest/board_webhook.go
func normalizeBoardRepo(p compassv1.ForgeProvider, raw string) string
```

Tests: a Linear `KEY-7` card carries `tracker` with all six fields, and
it survives `Rehydrate`; a GitHub card's `tracker` stays nil
(`issue_projection_test.go`); `RIG` keeps its case and `Owner/Repo` still
lowercases; two events on `RIG-7` coalesce to one read.

### T4 Linear board lane wiring (server, CLI)

- `refs.IsTeamKey` exports the tracker key grammar. `parseForgeLinearTeams`
  in `main.go` trims, upper-cases and validates each entry with it.
  `forgeEnvNames` in `go/e2e/fixture.go` gains `COMPASS_FORGE_LINEAR_TEAMS`.
  The manifest renderer is unchanged: it renders no Linear flag today.
- `reconcileForgeSeed` takes a `normalize` func; GitHub passes
  `normalizeGitHubRepo`, Linear passes `normalizeLinearTeam`.
- `buildDoors` builds one Linear client when tokens exist and passes it to
  `buildLinearWiring`. `buildLinearNotifyLane` takes that client instead of
  building its own.
- `buildLinearBoardLane` seeds the teams and builds the D1 lane with backstop
  0 (the ingest default). `buildLinearWiring` sets `dataSink` to a
  `fanoutSink` over the non-nil notify and board sinks.
- `serveDoors.linearBoard` holds the lane; `startForgeIngestLanes` starts its
  arm and reconciler on the serve group. A nil lane starts nothing.
- Before `startServeDoors`, `Serve` sets `board.Forges.LinearTeams` from the
  enabled Linear subscriptions. A team added at runtime needs a restart.
- `buildLinearWiring` logs one boot Warn when the board lane runs but the
  webhook handler is nil. The lane is then sweep-only, at 30-minute latency.
  Operator precondition: the Linear webhook subscribes to `Issue` events for
  every seeded team.

Interfaces:

```go
// go/internal/refs
func IsTeamKey(s string) bool

// go/cmd/compass-server/main.go
func parseForgeLinearTeams(raw string) ([]string, error) // "invalid --forge-linear-teams entry %q: want a Linear team key like RIG"

// go/server
// ForgeConfig gains: SeedLinearTeams []string
func normalizeLinearTeam(raw string) (string, error)
func reconcileForgeSeed(ctx context.Context, st *store.Store, provider store.ForgeProvider, host string, seed []string, normalize func(string) (string, error)) error
func buildLinearNotifyLane(st *store.Store, hub *runnerhub.Hub, client *forge.Linear, log *slog.Logger) *forgeNotifyLane
func buildLinearBoardLane(ctx context.Context, st *store.Store, issueBrd *board.IssueProjection, client *forge.Linear, seed []string, log *slog.Logger) (*boardIngestLane, error)
func buildLinearWiring(ctx context.Context, cfg ServeConfig, st *store.Store, hub *runnerhub.Hub, cm *comms.Comms, issueBrd *board.IssueProjection, serverResolver secrets.Resolver, adminID, bridgeID store.AccountID, tokens *linearagent.TokenSource, client *forge.Linear) (linearWiring, error)
// linearWiring gains: board *boardIngestLane; serveDoors gains: linearBoard *boardIngestLane
func startForgeIngestLanes(gctx context.Context, g *errgroup.Group, board, linearBoard *boardIngestLane, gitCredentials *gitCredentialBroker, notify ...*forgeNotifyLane)
func setCommsLinearTeams(ctx context.Context, c *comms.Comms, cfg ForgeConfig, st *store.Store) error
```

Tests: flag parsing (valid, lowercase input, too long, empty); the seed is
bootstrap-only; (pgtest) a sweep against the fake Linear upserts `RIG-1` and
`RIG-2` under provider 4 and advances the team watermark; a sweep whose fake
page 2 has `hasNextPage` with an empty cursor upserts no row and leaves the
watermark unchanged; a signed `Issue`
webhook through `NewLinearWebhookHandler` reaches both the notify sink and the
board sink, and the row changes; the GitHub sweep never lists `RIG`; seeding
`RIG` makes `GetRefLinkRules` return the Linear rule; a `KEY-N` board ref
resolves once `RIG` is enabled; with no webhook secret the Warn fires once.

### T5 Tracker state resolve (server; gated on OQ-1)

Follows the RIG-5077 consumer contract, amended by OQ-4(a).

```sql
-- 00NN_issue_tracker_resolved_state.sql (next free number)
-- The last Linear state id the board resolved (OQ-4); '' = never resolved.
ALTER TABLE issues ADD COLUMN tracker_resolved_state_id TEXT NOT NULL DEFAULT '';
```

- `store.Issue` gains `TrackerResolvedStateID`; the four T2 reads select it.
  The upsert never writes it.
- `MarkTrackerStateResolved` sets it `WHERE id = $1 AND tracker_state_id =
  $2`. Zero rows means a newer observation landed, and that one stays
  pending.
- `ListPendingTrackerIssues` returns the ids of provider-4 rows with a
  non-empty `tracker_state_id` that differs from the resolved id.
- `boardService.ResolveTrackerState`, under `transitionMu`: read the row;
  stop if the two ids match; call `resolve(row)`. On `ok=false`, or a `DONE`
  for an `ARCHIVED` row, move nothing. Else commit through the
  `SetIssueState` path without the mirror, because the state came from the
  tracker. Then mark the row's `tracker_state_id` resolved. `SetIssueState`
  and this method share one unlocked commit helper.
- `linearBoardSink.PublishIssueUpdate`: `PublishObservedIssue`, then
  `ResolveTrackerState` with `tracker.ResolveLinearStatus(cfg, row.Repo,
  {Name: row.TrackerStatus, Type: row.TrackerStateType})`. `cfg` is
  `store.DefaultTrackerConfig()` until RIG-5077 T3.
- `ResolvePending` runs once at lane start, before the first sweep. It
  covers the rows from T3–T4 and any resolve that a crash lost.
- `wireHubServiceCycles` returns its `*boardService`; `buildDoors` passes it
  to `buildLinearWiring` and `buildLinearBoardLane`, so one `transitionMu`
  covers every transition.

On main, only an agent's `SetIssueState` sets ARCHIVED. The guard stops a
later Linear close from undoing that.

Interfaces:

```go
// go/internal/store
// Issue gains: TrackerResolvedStateID string
func (s *Store) MarkTrackerStateResolved(ctx context.Context, id, stateID string) error
func (s *Store) ListPendingTrackerIssues(ctx context.Context) ([]string, error)

// go/internal/board
func (p *IssueProjection) PublishObservedIssue(ctx context.Context, issue *compassv1.Issue) (string, error) // row id; PublishIssueUpdate calls it

// go/server
// issueStore gains: MarkTrackerStateResolved(ctx context.Context, id, stateID string) error
func (b *boardService) ResolveTrackerState(ctx context.Context, issueID string, resolve func(store.Issue) (store.IssueState, bool)) (store.Issue, error)
type linearBoardSink struct { brd *board.IssueProjection; transitions *boardService; st *store.Store }
func (s *linearBoardSink) PublishIssueUpdate(ctx context.Context, issue *compassv1.Issue) error
func (s *linearBoardSink) ResolvePending(ctx context.Context) error
func wireHubServiceCycles(hub *runnerhub.Hub, commsSvc *comms.Comms, st *store.Store, issueBrd *board.IssueProjection) *boardService
// boardIngestLane gains: catchUp func(context.Context) error (nil for GitHub);
// startForgeIngestLanes runs it once before reconciler.Run.
// buildLinearWiring and buildLinearBoardLane gain: transitions *boardService
```

Tests: a first observation resolves; a comment-only update resolves nothing;
a state change resolves once; an older observation resolves nothing;
`ok=false` moves nothing and marks the row resolved; a tracker `DONE` on
`ARCHIVED` is a no-op; a failed commit leaves the row pending and the next
call resolves it; two concurrent observations end at the newer state;
(pgtest) a row ingested under T4 wiring lands `IN_PROGRESS` after
`ResolvePending`, and the Status `active` count includes it.

## Tasks

- [ ] T0 (OQ-1(d)): RIG-5077 T1 as frozen by the RIG-5100 ruling: Linear type constants, `store.TrackerConfig`, `tracker.ResolveLinearStatus`.
- [ ] T1: `forge.Issue` state fields, `state { id name type }`, `Linear.ListUpdatedIssues` with the OQ-2(c) cold-start filter, golden fixtures.
- [ ] T2: migration `0005`, tracker columns through the store and `IssuesByCoords`, coordinate-scoped `IsEnabledForgeRepo`, delete `ListEnabledForgeRepos`, adapters bind the coordinate.
- [ ] T3: `tracker` built in `translateOne` and rebuilt from the row, provider-aware `normalizeBoardRepo`.
- [ ] T4: `--forge-linear-teams`, one shared Linear client, `buildLinearBoardLane`, webhook fan-out, lane start, `LinearTeams` for board refs, sweep-only boot Warn.
- [ ] T5: `tracker_resolved_state_id`, `ResolveTrackerState` under `transitionMu`, `linearBoardSink`, boot `ResolvePending`, the archived guard, one shared `boardService`.
