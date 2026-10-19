# Repair required check runs left open after a pipeline ends (RIG-3462)

## Rulings

Matt ruled on 2026-10-18.

- **Q1 = A.** T1 indexes `pipelines.updated`. xorm `Sync` builds it at
  startup, and on Postgres the build blocks writes to `pipelines` once.
- **Q2 = B.** Repair `pending` and `absent`. A final state unlike the stored
  verdict gives `mismatch` and a `warn` line, with no write. A later record
  can move to repairing a mismatch if the `mismatch` count shows real cases.
- **Check runs** (2026-10-19, on this PR). The required gates move from
  commit statuses to check runs (RIG-5779, cut over in RIG-5844). The
  reconciler repairs the check runs. Q1 and Q2 carry over.

## Decisions

- **D1** (DL-565) — Owned runs: the `agg` run, and the `meta` run when
  `StatusMeta` is on and a pull request pipeline has a meta gate, matched by
  `external_id`. Per-workflow runs, informational runs and
  `pull_request_closed` pipelines are out of scope.
- **D2** (DL-566) — Repair a terminal pipeline's owned run that is
  `queued`, `in_progress` or absent (Q2 = B). Another conclusion gives
  `mismatch`. Liveness is never used.
- **D3** (DL-567) — Delivered state: one check-run list per owned name.
- **D4** (DL-568) — Supersede: a later owner in the store, a moved pull
  request head, or a newer run of another pipeline. The store and head
  checks run again under the key lock before the write.
- **D5** (DL-569) — Repairs use the cutover's check poster with one write
  attempt. A refresher skip returns `types.ErrSuperseded`.
- **D6** (DL-570) — Cadence `2m`. Candidates were updated in the last `3h` but not
  the last `2m`. `3h` covers a one-hour quota outage, as in RIG-3462, plus
  a restart.
- **D7** (DL-571) — Memory: one entry per pipeline, in process. A restart sweeps the
  lookback again.
- **D8** (DL-572) — Budget: at most `30` GitHub requests per pass, token mints
  and retries included. Quota below 20 % or a rate-limit error ends the pass.
- **D9** (DL-573) — The third failed write gives `gave_up`. Open work that leaves
  the lookback gives `expired`.
- **D10** (DL-574) — Metrics: a results counter, a calls counter, a backlog gauge.
- **D11** (DL-575) — A single server replica is a correctness requirement.
- **D12** (DL-576) — Tests use a hand-written fake forge. `MockForge` lacks optional
  capabilities.

## Problem / Intent

The cutover's `postAggregateCheck` writes the required runs. A write is
lost when `doForgeWrite` (server/forge/github/retry.go) gives up after 4
attempts in 30 s, or when a restart stops `reportForgeStatusAsync`. Both
callers only log. The run stays open and blocks the merge until a person
restarts the pipeline.

Intent: on a cadence, finish each terminal pipeline's owned runs that are
not completed on GitHub, at a bounded quota cost. Write coalescing is out
of scope.

## Approach

### Where it runs

`StatusReconciler` lives in server/pipeline/status_reconcile.go.
cmd/server/server.go runs it like the orphan reaper: a pass at start, one
per tick, a stop on `ctx.Done()`.

### Contract

It uses the cutover's contract (RIG-5844): App `4045728`, `external_id`
`wp1/<instance>/<number>/<kind>`, one run per (SHA, pipeline, kind),
PATCHed by id. Names come from `common.GetPipelineAggregateCheckName` and
`GetPipelineMetaCheckName`, the expected conclusion from
`aggregateCheckState`. RIG-5875's rulings on names and the canceled
conclusion need no reconciler change. It runs only when `StatusCarrier` is
`both` or `check`.

### One pass

1. Prune the memory. `GetTerminalPipelinesUpdated(now-lookback,
   now-settle)` gives the candidates, newest first. It uses `updated`:
   `UpdateToStatusDeclined` leaves `Finished` at `0`, and xorm sets
   `updated` on every `UpdatePipeline`.
2. Per repo, once per pass: `GetRepo`, `GetUser(repo.UserID)`,
   `Manager.ForgeFromRepo` and `forge.Refresh`. An inactive repo needs no
   forge call. No `forge.CheckReader` gives `unsupported`.
3. `GetPipeline`, then `ownedRuns` (D1).
4. Store check per run (D4). A later owner gives `superseded`.
5. Pull request pipelines: `PullRequestHead`. A moved head gives
   `superseded`; a pull request that is not open gives `closed`.
6. `DeliveredChecks` against `ExpectedCheck`. A newer run of another
   pipeline gives `superseded`. The pipeline's run completed with the
   expected conclusion gives `clean`, another conclusion `mismatch`, and
   `unknown` `incomplete`. `pending` and absent go on.
7. `forge.RepairAggregateCheck` or `forge.RepairMetaCheck`, fenced by
   steps 4 and 5 again, gives `reposted`, `superseded`, or `write_error`.
8. A failed read gives `read_error`. `types.ErrQuotaLow` and
   `types.ErrBudgetSpent` end the pass.

### Memory

Each entry holds the pipeline's `updated`; a change resets it.

- Done: `clean`, `reposted`, `mismatch`, `gave_up`, `unsupported`, a
  moved head, an inactive repo, or no owned run.
- Read again after 15 minutes: `superseded` by a later owner or the fence,
  `closed`, and `incomplete`, which is never written. Owners and pull
  requests change.
- Open, read next pass: `read_error`, `write_error`, and not reached. The
  third `write_error` gives `gave_up`.
- At pass start, an entry whose `updated` left the lookback is removed. An
  open one gives `expired`. The backlog gauge counts open entries.

### Budget

- `forge.WithReconcile(ctx, maxCalls)` marks the pass context and holds its
  budget. `rateLimitObserver.RoundTrip` (server/forge/github/ratelimit.go)
  wraps every GitHub client, the App JWT client too, and calls
  `forge.SpendCall` before each request, so retries and mints count.
- `Remaining` below 20 % of `Limit` on any response calls
  `forge.MarkQuotaLow`; the next `SpendCall` returns `types.ErrQuotaLow`
  and sends nothing. A rate-limit error does the same. The live path keeps
  the last fifth.
- Before each step the reconciler adds the step's reserve and ends the pass
  past `max-calls`: 1 per read, head read or write, 1 for a fence's head
  read, 1 for an absent run's fence re-list, plus 2 for a repo's first App
  token mint in a pass (`forgeCacheTTL` is 10 minutes).
- The worst pipeline costs 11: a mint, two reads, a head read, and two
  absent-run creates, each fenced by a head read and a re-list. `max-calls`
  is at least 11; the default `30` allows at most 900 requests an hour. A
  repair never neutralizes older runs.
- Servers on one App share each installation's limit: the floor sees the
  shared `Remaining`, but `max-calls` caps only this server.

### Races

- **Write order.** Every write to a run goes through the cutover's
  `deliver` (server/forge/github/status_dedupe.go) under the per-name key
  lock, with the refresher inside it. Live writes and repairs land in
  decision order with the same verdict, so the worst case is a duplicate.
- **A later pipeline on the same SHA.** It owns the name when it has the
  same commit and an event that renders the same name. Step 4 and the fence
  then skip. A pipeline created after the fence creates its own run under
  the key lock, after the repair, so the highest id stays the newest owner.
- **Another server's run.** The rpi5 and AKS servers post as one App with
  their own `<instance>` and their own store and lock, so neither later-owner
  check sees the other's pipelines. A run whose `external_id` names another
  instance is never this pipeline's run. For an absent run, the fence also
  lists the name again under the key lock and skips the create if any other
  run exists. What remains is a create landing between that list and the
  write. The live poster has the same window. Whether both servers can run
  pipelines for one repo at once is **unverified**; RIG-5844 owns that
  ordering. Until it is settled, enable the reconciler on one server only.
- **A moved pull request head.** Step 5 runs before the delivered read, so
  a stale-head pipeline is never `clean` or `mismatch`. The fence reads the
  head again under the key lock, just before the write. A head that moves
  after that read leaves the write on a SHA the merge no longer checks.
- **A stale `created` row.** A restart during creation leaves it; it never
  posts. Both later-owner checks ignore it after `CreationTimeout`.
- **Replicas (D11).** The key lock is per process. With two replicas, a
  repost is not ordered against the other replica's live write, and can
  hide a newer pending until that pipeline ends.

### Reading GitHub

`DeliveredChecks` (server/forge/github/check_read.go) uses
`requiredStatusClient`. Per owned name it reads one page of
`GET …/commits/{sha}/check-runs` with `app_id=4045728`, `check_name` and
`filter=all`; the default `latest` can hide the pipeline's own run. It
returns the run matching the `external_id` and whether a higher id belongs
to another pipeline. `total_count` above 100 without the run gives
`unknown`. `ExpectedCheck` is `aggregateCheckState`'s conclusion for the
stored verdict. `PullRequestHead` uses `common.UserToken`; a `404` is
`read_error`. Reads do not retry.

## Alternatives considered

- **Blind re-post.** A write per run per pipeline per pass on the
  secondary limit, over final states it cannot see.
- **Webhook-driven repair.** A lost write sends no `check_run` event.
- **A failure hook and a restart sweep.** It misses strands where no write
  was tried: RIG-1129, RIG-1170, and the meta tree self-load in
  `ReportMetaStatus`.
- **An out-of-server cron job.** It needs a second credential and a copy of
  the rollup, and cannot use the key lock.
- **An outbox table.** A store write per transition, and a write lost
  before its row commits still needs a reader.
- **Liveness.** True of every finished pipeline; the RIG-3462 triage rule
  rejects it.

## Global Constraints

- One PR on `rigel-release`, after the cutover's T1 lands. No API or web
  change, no new Go module. The only schema change is the index (Q1).
- Live write paths keep their retries, and a skip there still returns nil.
  `CHANGELOG.md` is not edited.
- With the interval above `0`, setup.go returns an error if the lookback is
  not above `2m` or max-calls is below `11`. server.go starts the
  reconciler only when `StatusCarrier` is `both` or `check` and
  `StatusAggregate` or `StatusMeta` is on. With `both`, the status leg is
  not repaired.
- New files have the Apache header, `Copyright 2026 Woodpecker Authors`.
  Commit the mocks from `make generate`.
- `testify` table tests. A test that swaps `server.Config` does not call
  `t.Parallel`.

The flags go after `pending-timeout` in cmd/server/flags.go:

```go
&cli.DurationFlag{
	Sources: cli.EnvVars("WOODPECKER_STATUS_RECONCILE_INTERVAL"),
	Name:    "status-reconcile-interval",
	Usage:   "interval for completing required check runs that a finished pipeline left open or absent; 0 disables",
	Value:   2 * time.Minute,
},
&cli.DurationFlag{
	Sources: cli.EnvVars("WOODPECKER_STATUS_RECONCILE_LOOKBACK"),
	Name:    "status-reconcile-lookback",
	Usage:   "how far back the status reconciler checks finished pipelines",
	Value:   3 * time.Hour,
},
&cli.IntFlag{
	Sources: cli.EnvVars("WOODPECKER_STATUS_RECONCILE_MAX_CALLS"),
	Name:    "status-reconcile-max-calls",
	Usage:   "maximum forge API requests in one status reconciler pass; at least 11",
	Value:   30,
},
```

## Length

About 2,800 prose words, over the 2,500 target. The extra is the numbered
test rows in T2 and T3 and the cross-server race. Each test row pins a
budget, fence or `external_id` edge that a mutation must fail.

## Plan

### T1. Store queries

Files: server/model/pipeline.go, server/store/store.go,
server/store/datastore/pipeline.go, server/store/datastore/pipeline_test.go,
server/store/mocks/mock_Store.go (generated).

Interfaces:

```go
// server/model/pipeline.go: the Updated tag gains INDEX (Q1).
Updated int64 `json:"updated" xorm:"INDEX 'updated' NOT NULL DEFAULT 0 updated"`

// server/store/store.go, after GetPipelineQueue

// GetTerminalPipelinesUpdated returns the terminal pipelines, except
// deployment and pull_request_closed, whose updated time is above after and
// not above before, newest first. Only ID, RepoID and Updated are set.
GetTerminalPipelinesUpdated(after, before int64) ([]*model.Pipeline, error)

// HasLaterPipeline reports whether p's repo holds a pipeline with p's commit,
// an event in events, and a higher number. It counts every status, except a
// created row whose created time is not above createdAfter: its creation
// stopped, so it never posts.
HasLaterPipeline(p *model.Pipeline, events []model.WebhookEvent, createdAfter int64) (bool, error)
```

- `GetTerminalPipelinesUpdated` uses `Cols("id", "repo_id", "updated")`,
  `builder.In` on the six statuses of `model.IsTerminalStatus`,
  `builder.NotIn` on `model.EventDeploy` and `model.EventPullClosed`, and
  `Desc("updated")`.
- `HasLaterPipeline` uses `Exist` and quotes `commit` as `getFeedSelect`
  does. An `error` row counts: `publishPipeline` still posts its aggregate.
- Tests with `newTestStore`. Rows are inserted with `NoAutoTime()`:
  1. `success` inside the window is returned. `running` is not.
  2. A row at exactly `before` is returned. A row at exactly `after` is not.
  3. `declined` with `Finished` `0` is returned. `deployment` and
     `pull_request_closed` are not.
  4. Rows come newest first, and `Commit` is empty.
  5. `HasLaterPipeline` is true for a higher number on the same commit and
     event, including an `error` row. It is false for another commit,
     event or repo, or a lower number.
  6. A `created` row counts when `created` is above `createdAfter`, and not
     at or below it.

### T2. Forge capability and repair posters

Files: server/forge/types/errors.go, server/forge/reconcile.go (new),
server/forge/check_reader.go (new), server/forge/forge.go,
server/forge/common/status.go, server/forge/common/status_test.go,
server/forge/github/status_aggregate.go, server/forge/github/retry.go,
server/forge/github/ratelimit.go, server/forge/github/check_read.go (new),
server/forge/github/check_read_test.go (new),
server/pipeline/helper_test.go, server/forge/mocks (generated).

Interfaces:

```go
// server/forge/types/errors.go
ErrQuotaLow    = errors.New("forge rate-limit quota is low")
ErrBudgetSpent = errors.New("forge call budget is spent")
ErrSuperseded  = errors.New("a later pipeline owns this check run") // replaces github.errSupersededReport

// server/forge/reconcile.go

// WithReconcile marks ctx as a reconciler call with a budget of maxCalls
// forge HTTP requests. The counter holds the requests sent under ctx.
func WithReconcile(ctx context.Context, maxCalls int64) (context.Context, *atomic.Int64)

// Reconciling reports whether ctx is a reconciler call.
func Reconciling(ctx context.Context) bool

// SpendCall charges one request to a reconciler ctx before it is sent. It
// returns types.ErrQuotaLow after MarkQuotaLow, or types.ErrBudgetSpent at
// maxCalls, and then charges nothing. It does nothing for other contexts.
func SpendCall(ctx context.Context) error

// MarkQuotaLow makes every later SpendCall on a reconciler ctx return
// types.ErrQuotaLow. It does nothing for other contexts.
func MarkQuotaLow(ctx context.Context)

// server/forge/check_reader.go

// DeliveredUnknown is the state of a run that a capped read did not reach.
const DeliveredUnknown = "unknown"

// DeliveredCheck is what GitHub holds for one owned run name.
type DeliveredCheck struct {
	// State is "pending" (queued or in_progress), the conclusion of a
	// completed run, "" when the pipeline has no run, or DeliveredUnknown.
	State string
	// RunID is the pipeline's run, 0 when State is "" or DeliveredUnknown.
	RunID int64
	// Newer is true when a run with a higher id has another external_id.
	Newer bool
}

// CheckReader is an optional Forge capability for the status reconciler.
type CheckReader interface {
	// DeliveredChecks reads each name in names on commit and matches the
	// run whose external_id is externalIDs[name].
	DeliveredChecks(ctx context.Context, u *model.User, r *model.Repo, commit string, names, externalIDs map[string]string) (map[string]DeliveredCheck, error)
	// ExpectedCheck returns the conclusion the poster writes for the
	// terminal pipeline p, with p.Workflows loaded for pull request events.
	ExpectedCheck(p *model.Pipeline, meta bool) string
	// OwnedCheckRuns returns p's run names and external_ids by kind, from
	// the cutover's name helpers and aggregateExternalID.
	OwnedCheckRuns(r *model.Repo, p *model.Pipeline, meta bool) (names, externalIDs map[string]string)
	// PullRequestHead returns the head SHA of p's pull request and whether
	// it is open.
	PullRequestHead(ctx context.Context, u *model.User, r *model.Repo, p *model.Pipeline) (head string, open bool, err error)
}

// server/forge/common/status.go, after GetPipelineAggregateCheckName

// GetAggregateCheckNameEvents returns each event whose aggregate check name
// for repo renders the same non-empty string as p's. It tries every event
// except deployment, which posts no aggregate. A template error gives nil.
func GetAggregateCheckNameEvents(repo *model.Repo, p *model.Pipeline) []model.WebhookEvent

// server/forge/forge.go

// HasLaterMetaPipeline is hasLaterMetaPipeline, exported. It also skips a
// created row whose Created is not above createdAfter, as
// store.HasLaterPipeline does. ReportMetaStatus passes 0: today's behaviour.
func HasLaterMetaPipeline(s store.Store, r *model.Repo, b *model.Pipeline, createdAfter int64) (bool, error)

// RepairAggregateCheck finishes b's agg run with its verdict through
// postAggregateCheck: a PATCH of runID, or a guarded create when runID is 0.
// Its refresher calls later under the key lock, before the write. It does
// not neutralize older runs. It returns types.ErrSuperseded when later is
// true, and types.ErrNotImplemented when f has no AggregateStatusReporter.
func RepairAggregateCheck(ctx context.Context, f Forge, s store.Store, u *model.User, r *model.Repo, b *model.Pipeline, runID int64, later func() (bool, error)) error

// RepairMetaCheck does the same for b's meta run. It skips the early check
// of ReportMetaStatus: the reconciler has just made it.
func RepairMetaCheck(ctx context.Context, f Forge, s store.Store, u *model.User, r *model.Repo, b *model.Pipeline, runID int64, later func() (bool, error)) error

// server/forge/github/ratelimit.go
const reconcileQuotaFloor = 0.2

// RoundTrip calls forge.SpendCall(req.Context()) first, and returns its error
// without sending. When forge.Reconciling(req.Context()) and
// quotaLow(resp.Header), it calls forge.MarkQuotaLow.
func (o *rateLimitObserver) RoundTrip(req *http.Request) (*http.Response, error)

// quotaLow reports whether h shows X-RateLimit-Remaining below
// reconcileQuotaFloor of X-RateLimit-Limit. Without a Limit it is false.
func quotaLow(h http.Header) bool

// server/forge/github/retry.go: signature unchanged. When
// forge.Reconciling(ctx), it makes one attempt and returns quotaErr(err).
// That covers the token mint in requiredStatusClient and the check write
// in postAggregateCheck.
func doForgeWrite(ctx context.Context, fn func() (*github.Response, error)) (*github.Response, error)

// server/forge/github/status_aggregate.go

// ignoreSkip drops types.ErrSuperseded and errUnchangedStatus. It keeps
// types.ErrSuperseded when forge.Reconciling(ctx).
func ignoreSkip(ctx context.Context, err error) error

// server/forge/github/check_read.go
const checkRunsPerPage = 100 // one page per name

// DeliveredChecks lists ListCheckRunsForRef per name within
// statusReportTimeout with AppID, CheckName and Filter "all". A run not on
// the page is "" when TotalCount fits the page, else DeliveredUnknown.
func (c *client) DeliveredChecks(ctx context.Context, u *model.User, r *model.Repo, commit string, names, externalIDs map[string]string) (map[string]forge.DeliveredCheck, error)
func (c *client) ExpectedCheck(p *model.Pipeline, meta bool) string
func (c *client) OwnedCheckRuns(r *model.Repo, p *model.Pipeline, meta bool) (map[string]string, map[string]string)

// PullRequestHead parses the number from p.Ref (headRefs, mergeRefs) and
// calls PullRequests.Get. Another ref is an error.
func (c *client) PullRequestHead(ctx context.Context, u *model.User, r *model.Repo, p *model.Pipeline) (string, bool, error)

// quotaErr wraps a *github.RateLimitError or *github.AbuseRateLimitError
// as types.ErrQuotaLow. It returns other errors unchanged.
func quotaErr(err error) error
```

- Each `Report*` and `Repair*` pair shares one unexported helper.
- GitHub tests use an `httptest` server and a real client, as
  `TestRequiredStatusesUseAppToken` does; a `githubClientKey` client skips
  `rateLimitObserver`:
  1. The request carries `app_id=4045728`, `check_name` and `filter=all`.
     `queued` and `in_progress` give `pending`; a completed run gives its
     conclusion and id; no run gives `""`.
  2. A run of the same name with another instance's `external_id` is not
     matched. A higher id with another `external_id` sets `Newer`.
  3. `total_count` 150 without the run gives `DeliveredUnknown` after one
     request. `total_count` 3 without it gives `""`.
  4. Page 1 at `Remaining` 900 of 5000, with a second name left: the second
     request is not sent, and the read gives `ErrQuotaLow`. At 1100 it is.
  5. A `403` with `X-RateLimit-Remaining: 0` gives `ErrQuotaLow` on a read
     and on a repair write, which is sent once.
  6. An open pull request gives its head and `true`, a closed one `false`,
     a `404` an error.
  7. `ExpectedCheck` equals `aggregateCheckState`'s conclusion for every
     terminal `StatusValue`; meta with a failed gate gives `failure`.
  8. A repair with a run id sends one PATCH to that id and lists nothing.
     With id 0 and no later pipeline it sends one create. Neither touches
     another run.
  9. A `500` on a repair write: one request and an error. Live: four.
  10. A refresher skip on a meta repair: `ErrSuperseded`, no write. Live: nil.
  11. An App with an empty token cache: the mint sends 2 requests before the
      read. A budget of 3 reads one name and gives `ErrBudgetSpent` on the
      second; 4 reads both.
  12. An empty token cache and a `GetRepositoryInstallation` response at
      900 of 5000: `ErrQuotaLow` with no `CreateInstallationToken` and no
      read, counter 1. A second read sends nothing.
- status_test.go: `GetAggregateCheckNameEvents` gives only `push` for a
  push pipeline with the default format, every event but `deployment` when
  the format has no `{{ .event }}`, and nil for a template error.
- helper_test.go: `HasLaterMetaPipeline` ignores a later `created` row at
  or before `createdAfter`; `ReportMetaStatus` passes 0 and still skips.
  With `aggregateResilienceForge`, each `Repair*` puts `later` in the
  refresher, with no early call.

### T3. The reconciler

Files: server/pipeline/status_reconcile.go (new),
server/pipeline/status_reconcile_test.go (new), server/config.go,
cmd/server/flags.go, cmd/server/setup.go, cmd/server/server.go,
docs/docs/30-administration/10-configuration/10-server.md,
docs/docs/30-administration/10-configuration/12-forges/20-github.md.

Interfaces:

```go
// server/pipeline/status_reconcile.go
const (
	statusReconcileSettle  = 2 * time.Minute
	statusReconcileRecheck = 15 * time.Minute // superseded, closed and incomplete are read again after this
	checkReadReserve       = 1                // one check-run page per owned name
	mintReserve            = 2                // App token mint, added to a repo's first read in a pass
	headReadReserve        = 1                // PullRequestHead; a pull request write adds one for its fence
	relistReserve          = 1                // an absent run's fence lists the name again
	repairWriteReserve     = 1
	repairMaxWrites        = 3 // the third write_error gives gave_up
)

var (
	statusReconcileResults = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "woodpecker",
		Name:      "status_reconcile_results_total",
		Help:      "Owned check runs the reconciler checked, by result. expired counts pipelines.",
		// result: clean, reposted, mismatch, superseded, closed, unsupported,
		// read_error, incomplete, write_error, gave_up, expired.
	}, []string{"result"})
	statusReconcileCalls = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "woodpecker",
		Name:      "status_reconcile_calls_total",
		Help:      "Forge HTTP requests the reconciler sent.",
	})
	statusReconcileBacklog = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "woodpecker",
		Name:      "status_reconcile_backlog",
		Help:      "Pipelines with repair work left after the last pass.",
	})
)

type ownedRun struct {
	name       string
	externalID string
	meta       bool
	owners     []model.WebhookEvent // aggregate only: common.GetAggregateCheckNameEvents
}

// repoPass is one repo's state in one pass (step 2).
type repoPass struct {
	forge  forge.Forge
	reader forge.CheckReader
	user   *model.User
	repo   *model.Repo
	minted bool // a read ran in this pass, so the App token mint is paid
}

// reconcileEntry is the memory for one pipeline. A pipeline is skipped while
// updated is unchanged, open is false, and until is zero or in the future.
type reconcileEntry struct {
	updated int64     // the pipeline's updated time when the entry was written
	until   time.Time // zero: done; else read again after this time
	open    bool      // repair work is left
	writes  int       // failed repair writes
}

type StatusReconciler struct {
	store           store.Store
	lookback        time.Duration
	creationTimeout time.Duration
	maxCalls        int64
	entries         map[int64]*reconcileEntry // by pipeline ID
}

func NewStatusReconciler(s store.Store, lookback, creationTimeout time.Duration, maxCalls int) *StatusReconciler

// Run does one pass. It returns an error only when the candidate query fails.
// It calls forge.WithReconcile once and adds the counter to
// statusReconcileCalls. createdAfter is now minus creationTimeout; setup.go
// always sets CreationTimeout.
func (r *StatusReconciler) Run(ctx context.Context, now time.Time) error

// ownedRuns applies D1 through reader.OwnedCheckRuns: deployment and
// pull_request_closed pipelines own no run. For pull request events it
// loads the tree into p.Workflows, as ReportAggregateStatus does. An empty
// name (a template error) is skipped.
func ownedRuns(s store.Store, rp *repoPass, p *model.Pipeline) ([]ownedRun, error)

// fence returns the later check passed to the repair: step 4 again
// (s.HasLaterPipeline with run.owners, or forge.HasLaterMetaPipeline), for a
// pull request pipeline PullRequestHead again, and, when runID is 0,
// DeliveredChecks for run.name again. It is true when a later pipeline owns
// run, the head is not p.Commit, the pull request is not open, or any run
// of the name now exists.
func fence(ctx context.Context, s store.Store, rp *repoPass, p *model.Pipeline, run ownedRun, runID int64, createdAfter int64) func() (bool, error)

// reconcilePipeline does steps 3 to 7 for p and updates e. It returns
// types.ErrQuotaLow and types.ErrBudgetSpent unchanged, and Run ends the pass
// on them.
func (r *StatusReconciler) reconcilePipeline(ctx context.Context, rp *repoPass, p *model.Pipeline, e *reconcileEntry, used *atomic.Int64, now time.Time) error
```

```go
StatusReconcileInterval time.Duration // server/config.go, after PendingTimeout
StatusReconcileLookback time.Duration
StatusReconcileMaxCalls int

server.Config.Server.StatusReconcileInterval = c.Duration("status-reconcile-interval") // cmd/server/setup.go
server.Config.Server.StatusReconcileLookback = c.Duration("status-reconcile-lookback")
server.Config.Server.StatusReconcileMaxCalls = c.Int("status-reconcile-max-calls")

reconciler := pipeline.NewStatusReconciler(_store, server.Config.Server.StatusReconcileLookback, // cmd/server/server.go
	server.Config.Server.CreationTimeout, server.Config.Server.StatusReconcileMaxCalls)
```

- Logs: `info` per `reposted` (repo, pipeline, SHA, name, run id,
  conclusion); `warn` per `mismatch`, `incomplete`, `gave_up`, `expired`,
  and a pass that budget or quota ends.
- Repairing a final mismatch later changes only the step-6 branch and test row 2.
- Docs: the three `STATUS_RECONCILE_*` sections go after
  `### STATUS_CARRIER`, with the defaults, `0` disables, the two repaired
  runs, the carrier values it runs under, and one replica. The GitHub page:
  the App needs **Checks** read and write, and servers on one App share its
  rate limit.

### T3 tests

`TestStatusReconciler` rows use a strict `store_mocks.MockStore`,
`manager_mocks.MockManager`, and a fake `reconcileForge` that embeds
`forge.Forge` and implements both reporters and `CheckReader`. It calls
`forge.SpendCall` once per call unless a row says otherwise, and its
posters call the refresher. Counters use `testutil.ToFloat64`.

1. A push pipeline whose `agg` run is `in_progress`: one PATCH of that run
   id, `reposted` 1, an `info` line. A second `Run` makes no call.
2. Absent: one guarded create. Completed with the expected conclusion:
   `clean`. Another conclusion: `mismatch`, no write, a `warn` line.
   `DeliveredUnknown`: `incomplete`, no write, a `warn` line. A `Run` within
   15 minutes does not read it; a `Run` after 15 minutes reads it again.
3. `HasLaterPipeline` true: `superseded`, no forge call. Its events: every
   event but `deployment` when the name has no `{{ .event }}`, only `push`
   with the default format. `Newer` true with the pipeline's run open:
   `superseded`, no write.
4. A pull request pipeline whose head moved: `superseded`, no
   `DeliveredChecks` call. Closed: `closed`. A `PullRequestHead` error:
   `read_error`, read again next `Run`.
5. A meta-gated pull request pipeline with both runs open: both repaired.
   With `StatusMeta` off, only `agg` is read.
6. The repair returns `types.ErrSuperseded`: `superseded`, no `reposted`,
   no `info` line.
7. The write fails in three passes: `write_error` twice, then `gave_up`. A
   fourth `Run` makes no call.
8. `maxCalls` 12, one repo: the first read counts 3 (a mint), later reads
   1, a PATCH 1. Of six push pipelines with `in_progress` runs, the sixth
   is not read; calls 12, backlog 1.
9. The first read returns `ErrQuotaLow`: no other forge call, a `warn` line.
   A read that spends past `maxCalls` gets `ErrBudgetSpent` and ends the
   pass.
10. No `CheckReader`: `unsupported`. An inactive repo, or a
    `pull_request_closed` or `deployment` pipeline: no forge call.
11. A new `StatusReconciler` reads a pipeline the old one repaired. A
    changed `updated` also makes it read again.
12. Store `superseded`: skipped for 15 minutes, then read. With
    `HasLaterPipeline` now false, it is repaired.
13. An open pipeline whose `updated` leaves the lookback: `expired` 1, a
    `warn` line, the entry removed.
14. The head matches at step 5 and moves before the write: the fence sees
    it. `superseded`, no write.
15. An open run whose `external_id` names the other instance is not this
    pipeline's run: the pipeline's run reads as absent.
16. No owned run and `Newer` true: `superseded`, no create.
17. No run at the read, and the fence's re-list finds a run of the name:
    `superseded`, no create.

## Tasks

- [x] Matt answers Q1 (A) and Q2 (B).
- [ ] RIG-5844 T1 lands on `rigel-release`; RIG-5875 rules on names (Q3)
      and the canceled conclusion (Q2).
- [ ] T1, T2, T3 in order: tests fail first, then the code.
- [ ] `make generate`, `make lint` and `make test-server` pass.
- [ ] On the CI deployment (one replica, one server until RIG-5844 settles
      cross-server ordering), set the interval.
- [ ] After deploy: `reposted` lines name pipelines whose checks then go
      final; `status_reconcile_calls_total` grows at most `max-calls` per
      interval; the App's `woodpecker_github_ratelimit_remaining` shows no
      new dip. Record the `mismatch` count.
