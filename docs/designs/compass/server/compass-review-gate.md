# Compass-dispatched review gate

Tracking: RIG-6010
Ledger: DL-641, DL-642

## Open Questions

These questions are load-bearing working assumptions, not frozen decisions. RIG-6066 rules them; when its rulings are folded in, DL-641, DL-642 and the DL-133 clause suffix change to `(Matt, date)`.

1. **Product-owner review:** Who supplies the review, and what does it cover? `docs/concepts/agent-roles.md` defines supervisor, owner, and manager, but no CTO/product-owner role. RIG-6011 designs the flatter tree and coordinator/CTO split; this record depends on it and does not design that agent. Option A maps each repository to a named RIG-6011 reviewer who checks approved issue intent, design, and roadmap impact. Option B uses a human final pass, which is clear but serial. **Recommendation:** A, in parallel with code review, after RIG-6011 defines identity and repository mapping.
2. **Merge identity and entrypoint:** Option A uses a Compass Server App and Server-internal action, never an agent-callable merge arm; this is narrow but requires new provider and Server work. Option B gives a designated agent identity a new `ForgeCallRequest` merge arm; it reuses the agent-call path but broadens mutation authority. **Recommendation:** A, with least-privilege forge permissions. Compass has no PR merge capability today: `forgeService.transitionPullRequestState` in `go/server/forge.go` says merging is separate, and `ForgeCallRequest` in `proto/compass/v1/agent_gateway.proto` has no merge arm.
3. **Verdict evidence:** Option A uses visible GitHub reviews and checks but binds the gate to the forge. Option B stores native Server verdicts, which is portable but adds a second authority and consistency work. **Recommendation:** A now; store the Server decision and evidence references, and defer a native review surface to a later record.
4. **Risk-rubric source:** Option A versions the rubric in Server configuration, centrally controlled but less local. Option B uses per-repository policy, reviewable with code but changeable by the PR being judged. **Recommendation:** A; repository policy may narrow eligibility only.
5. **Migration trigger:** Option A opts in each repository after policy, checks, audit, and kill-switch readiness; staged but coordinated. Option B enables all repositories in a tenant at once; simpler but ignores repository differences. **Recommendation:** A.
6. **Reviewer boundary:** Option A has the Server enforce outside-author-subtree membership; consistent but needs ancestry reads at selection and completion. Option B leaves it to policy; simpler but prone to drift. **Recommendation:** A, failing closed on unknown ancestry.
7. **Risk classifier:** Option A requires a named non-author reviewer to return risk class and rationale; the Server enforces path exclusions, policy version, and checks, while uncertainty goes to a human. Option B reserves classification to a human; safer but serial. Option C has the Server classify all risk; deterministic but cannot judge intent, reversibility, or test adequacy. **Recommendation:** A. The author and authoring Manager never classify their own PR.
8. **Rebase coverage and queue action:** Option A voids all verdicts on any new SHA, including an empty-interdiff rebase; simple but repeats review. Option B carries code review only if the diff is proven unchanged against the new base; checks and action authorization stay fresh, but equivalence proof adds machinery. Option C carries verdicts across any conflict-free rebase; cheaper but does not prove unchanged content. `PullRequest.merge_queue` (`MergeQueueEntry`) in `proto/compass/v1/compass.proto` represents queue state; today an operator enqueues and the queue merges. **Recommendation:** A for coverage. For queue-managed repositories, Server “merge” means enqueue after fresh head, checks, policy, and audit; queue candidate checks remain additional. Never direct-merge or bypass the queue. Repositories without a supported queue remain human-merged.
9. **Kill switch:** Option A stores a tenant-wide switch in Server configuration; simple but may need restart. Option B stores a per-repository switch in Postgres; isolates failures but not broad incidents. Option C stores both in Postgres. **Recommendation:** C, checked before every action; only an authenticated human operator can change it through an operator-only Server path.

## Problem / Intent

Today the authoring agent starts review, gathers reviewers, and reports its own all-clear. Compass needs an agent-initiated MVP that can move to Server-dispatched review, with explicit verdict coverage and a Server-confirmed gate. A later, bounded path may let the Server enqueue eligible implementation PRs. Design PRs and escalations keep the human gate.

This supersedes the exact sentence in `docs/concepts/review-flow.md`:
> The human is **always** the final merge gate, regardless of how many Managers review — an agent approval is a "review passed" signal, never the merge itself.

It also supersedes the clause-level human-merge statements in `docs/concepts/README.md`, root `README.md`, `config/rules/never-merge.md`, `config/rules/hold-your-lane.md`, `config/skills/review/SKILL.md`, `config/skills/jj/SKILL.md`, and `config/prompts/{manager,owner,supervisor}/SYSTEM.md`. This applies only to eligible, Server-handled implementation PRs; designs and escalations remain human-approved and human-merged. DL-133's status records this clause-level supersession; its other clauses remain Active.

## Approach

### Grounded starting point

Compass paths below are relative to the Compass repository root.

`ForgeCallRequest.submit_review` in `proto/compass/v1/agent_gateway.proto` carries agent reviews. `forgeService.ExecuteForgeCallAsAccount` and `forgeService.submitReview` in `go/server/forge.go` dispatch through the shared Reviewer App (`go/server/serve.go`). `forge.Review` in `go/internal/forge/provider.go` lacks a review ID; add one and add `Review.id = 6` in `proto/compass/v1/compass.proto`. A review counts for an agent only when its ID joins a `done` `WriteKindReview` intent's `forge_key`, whose `agent_account_id` matches the required reviewer. Resolve the PR author only from `forge_authored_artifacts.agent_account_id`, never body attribution: `StripOwner` in `go/internal/forge/owner.go` calls it a display claim. Human (non-App) reviews count only when policy names that human.

`forge.GitHub.GetPullRequest` in `go/internal/forge/github.go` reads head SHA, reviews, checks, and threads. `PullRequest.head_sha` and `Review.commit_id` in `proto/compass/v1/compass.proto` carry head-bound evidence. `ParseGitHubEvent` and `NotifyRouter.Route` in `go/internal/forge/githubapp_webhook.go` and `go/internal/ingest/notify_router.go` deliver forge changes; events schedule reconciliation but are not evidence. `forgeService.transitionPullRequestState` in `go/server/forge.go` changes open/closed state only. Compass has no PR merge or enqueue capability today.

`forge_get_pull_request` in `packages/compass-agent/src/forge.ts` is the existing agent read interface for PR reviews and threads. The status model in `docs/designs/compass/agent/compass-agent-status-record/design.md` treats a review as current when `Review.commit_id` equals `PullRequest.head_sha` and older approvals as stale (DL-450).

The author relationship is stored in `parent_agent_id`; `Store.AgentSubtree` and its recursive query expose descendants (`go/internal/store/agent_tree.go`, `go/internal/store/queries/agent_tree.sql`). Spawn sets the caller as parent (`go/server/lifecycle.go`). The existing delivery path is `comms.PostAsAccount` and `runnerhub.Hub.DispatchControl` (`go/internal/comms/agent_caller.go`, `go/internal/runnerhub/dispatch_control.go`). Reviewers must exclude the author and the author's descendants.

### Phased operating model

1. **Agent-initiated MVP:** the authoring Manager dispatches code reviewers and the product-owner reviewer on the current head. Reviewers use `submit_review`; the Manager reads `forge_get_pull_request` and reports whether all required verdicts and checks cover that SHA. The report remains self-reported, not a Server completion decision.
2. **Server-confirmed review:** the Server creates one durable run per PR head and reviewer set, dispatches reviewers, and reconciles fresh forge state. `NotifyRouter.Route` schedules reconciliation once per event, not per subscriber. Agents can read status but cannot complete a run or waive a reviewer. Human merge approval remains required.
3. **Bounded implementation action:** after repository opt-in, the Server may approve and enqueue an eligible implementation PR. It re-reads the PR and reviews, requires explicit current required checks and reviewer evidence, applies mechanical exclusions and the recorded risk result, commits an audit receipt before calling the forge, and never bypasses the queue. If evidence changes or is unknown, it escalates to the human. Design records, policy changes, security-sensitive work, and elevated or uncertain risk remain human-approved and human-merged.
4. **Native review surface:** a later record may replace forge-specific verdict evidence with native review records. Until then, GitHub is the evidence source; the Server stores its decision and evidence references, not a second reviewer authority.

### Review and risk policy

Each run names required reviewer agent IDs and roles. Code reviewers check implementation correctness. The product-owner review checks approved issue intent, design, and roadmap impact; identity and repo mapping depend on RIG-6011. Reviewers work in parallel on one head. Only `approve` passes; `request_changes` blocks until that reviewer later approves. Comments, missing responses, failed dispatch, or unavailable reviewers are incomplete. The author cannot waive or replace reviewers. The Server excludes the author's subtree at selection and completion. Count an agent review only through the review-ID-to-done-intent join with the required reviewer account ID; never infer identity from the shared App login or body. Count a human review only if policy names that human.

Every new SHA invalidates verdicts, checks, risk classification, and action authorization, including empty-interdiff rebases. GitHub stamps a review with the current head when no commit is sent, so `submit_review` must carry the head the reviewer read. A verdict counts only when the SHA recorded on its review intent, `Review.commit_id` and `PullRequest.head_sha` all match. Re-fetch before completion and action. Required checks must be green on the head; missing configuration, unknown or stale evidence, or blocking threads fails closed. Queue checks are additional.

A required non-author reviewer assesses the full diff, linked issue and design, dependencies, tests, rollback, and behavior, then returns an enumerated risk class and short rationale. The Server does not infer semantic risk. It rejects mechanically excluded paths and categories, verifies policy version and current checks, and escalates high risk, unknown classification, weak evidence, or disagreement. Exclusions include design records, gate policy, credentials, authentication or authorization, branch protection, security boundaries, destructive production work, difficult-to-reverse migrations, financial commitments, external contracts, and mixed-scope PRs. A reviewer cannot override a mechanical exclusion.

The Server records each review run as one PR-head review request with required reviewers, dispatch state, and evidence. A gate decision records the Server's outcome for that run and SHA. An audit receipt records each forge action and its evidence.

### Implementation queue action

After repository opt-in, the Server may approve and enqueue an eligible implementation PR. The forge provider's approval and enqueue operations are new work. Each must use the expected head SHA; if the provider cannot prove head stability, fail closed and escalate. The Server never direct-merges or bypasses the queue. `MergeQueueEntry` records observed queue state, not an enqueue API. Queue candidate checks are an additional gate.

### Durable state, audit, and recovery

A gate decision records the Server outcome for a review run and head SHA. Every new primary or unique key starts with `tenant_id`. T3 adds `review_runs`, unique on `(tenant_id, forge_provider, forge_host, repo, pr_number, head_sha, policy_version)`; `review_run_reviewers`, keyed by `(tenant_id, run_id, reviewer_account_id)`; and `review_gate_decisions`, keyed by `(tenant_id, decision_id)`. They store dispatch state, verdict and SHA, risk, rationale, required-check and reviewer evidence, and time. Persist queued dispatch before delivery; reconcile failed deliveries idempotently. T4 adds append-only `review_gate_audit` keyed by `(tenant_id, action_id)` with decision, operation, actor, PR coordinate, SHA, changed-file digest, policy, evidence, state, timestamps, and forge result. `review_gate_controls` is keyed by `(tenant_id, scope)`; scope is tenant-wide or repository. Controls store enabled state, updater, time, and reason.

The Server commits an audit receipt before approval or enqueue; a write failure blocks the forge call. If action succeeds but receipt completion fails, the prepared row remains; the next sweep reads forge state and reconciles it. Retry only when the forge proves the action did not occur and it remains valid. Authenticated human operators read receipts through an operator-only surface. Agents see gate status and escalation reason, not audit. Check tenant and repository Postgres switches before every action.

## Alternatives considered

Keeping the authoring Manager as final authority leaves completion self-reported. Treating events or summaries as evidence risks stale state. Carrying review across every rebase does not prove equivalent content. An agent-callable merge arm grants too broad a mutation surface. Direct merge ignores the existing operator-enqueued queue path. The phased approach preserves today's forge evidence, makes the Server authoritative before permitting queue action, and leaves uncertain cases with a human.

## Plan

### Global Constraints

- GitHub remains the evidence source until a later native-review design. Preserve portability at the provider seam.
- Every verdict and action binds to an exact head SHA. Any new SHA invalidates coverage and action authorization.
- The author never classifies or approves its own PR. Reviewer selection and completion fail closed on unknown ancestry.
- Events trigger reconciliation only. Current forge reads are authoritative; unknown or missing required checks block.
- No direct merge or queue bypass. Queue-managed repositories may use Server-authorized enqueue after readiness; unsupported repositories retain human merge.
- Design records, policy changes, security-sensitive work, and uncertain or elevated risk remain human-approved and human-merged.
- Commit an audit action before forge mutation. Audit failure blocks; reconcile a post-action receipt failure from the forge on the next sweep.
- Tenant-wide and per-repository kill switches are checked before every action. Only an authenticated human operator may change them.
- Keep this public record focused on Compass core behavior; managed-service deployment and rollout are out of scope.

### Ordered implementation plan

1. Rule OQ-1–OQ-9 in RIG-6066 and fold the rulings in before merge.
2. Add agent-initiated gate status and update review guidance. Keep its result agent-reported; Manager supplies required reviewer agent IDs.
3. Add Server-owned review-run persistence, idempotent dispatch, reconciliation, and status. Test subtree exclusion, retries, changed heads, missing verdicts, checks, and threads.
4. Add mechanical exclusions, tenant/repository controls, queue readiness, audit-before-action, Server identity, and enqueue. Test races, denial, audit recovery, kill switch, and escalation.
5. Update affected concepts, README, rules, skills, and prompts. Document recovery; defer native reviews.

- [ ] T1 — Rule OQ-1–OQ-9 in RIG-6066 and fold the rulings into this record and DL-641/DL-642 before merge; then set DL-641, DL-642 and the DL-133 clause suffix to `(Matt, date)`. **Interfaces:** Open Questions; RIG-6011 identity and repository mapping.
- [ ] T2 — Add read-only review status; `submit_review` remains an ack. Manager supplies reviewer agent IDs in `ReviewGateStatusRequest{repo string, prNumber uint64, requiredReviewerIDs []string}`; return current SHA, reviewers, verdicts, checks, threads, and completion. Add request arm `review_gate_status = 26`, result arm `review_gate_status = 16`, and an `ExecuteForgeCallAsAccount` case. T2 adds review ID to `forge.Review` and `Review.id = 6` in the proto, and a required `SubmitReviewRequest.commit_id = 6` that `forge.SubmitReview` sends as GitHub's `commit_id`; a migration adds `head_sha` to `forge_write_intents` and `store.WriteIntent`, and `forge_submit_review` in `packages/compass-agent/src/forge.ts` gains a required `commit_id` parameter. **Interfaces:** `func (s *forgeService) ReviewGateStatus(ctx context.Context, caller store.AccountID, req ReviewGateStatusRequest) (ReviewGateStatus, error)` reads `GetPullRequest` and joins each head-current review ID to a done review intent owned by its required reviewer; T3 adds persisted status.
- [ ] T3 — Add Server review runs, tenant-first keys, idempotent dispatch via `comms.PostAsAccount`/`runnerhub.Hub.DispatchControl`, and head reconciliation. Extend `gitHubStateOrUpdateKind` for `synchronize` and `ready_for_review`; sweeps compare open-run SHA with forge head and start a new run on change. Schedule once per event at `NotifyRouter.Route`. **Interfaces:** tenant-scoped `CreateReviewRun`, `UpdateReviewReviewer`, and `ListPendingReviewRuns`; `StartReviewRun`, `ReconcileReviewRun`, `GetReviewGateStatus`. Persist dispatch before delivery and retry idempotently. T1 finalizes OQ-1/OQ-3/OQ-6/OQ-7.
- [ ] T4 — Add tenant/repository controls, audited Server approval and enqueue. Use tenant-first keys; add provider approval/enqueue operations and operator-only control handler, not an agent-callable merge arm. **Interfaces:** `func (s *Store) RecordGateDecision(ctx context.Context, in NewGateDecision) (GateDecision, error)`; `func (s *Store) PrepareGateAction(ctx context.Context, in NewGateAction) (GateAction, error)`; `func (s *Store) CompleteGateAction(ctx context.Context, tenantID string, actionID uuid.UUID, result GateActionResult) error`; `func (s *Store) ListUnreconciledGateActions(ctx context.Context, tenantID string, limit int) ([]GateAction, error)`; `func (s *Store) SetReviewGateControl(ctx context.Context, in ReviewGateControl) error`; `forge.Provider.ApprovePullRequest` and `EnqueuePullRequest` take `expectedHeadSHA`; `reviewGateService.ActOnGateDecision` and operator-only `SetControl`. Commit audit before forge action; reconcile prepared receipts on sweep. T1 finalizes OQ-2/OQ-4/OQ-5/OQ-8/OQ-9.
- [ ] T5 — Update affected concepts, README, `config/rules/never-merge.md`, `config/rules/hold-your-lane.md`, skills, and prompts. **Interfaces:** review scoped claims against source.
