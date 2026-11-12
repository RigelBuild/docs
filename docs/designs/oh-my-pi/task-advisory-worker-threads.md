# Task advisory across worker-thread subagents

Linear: RIG-9802. Direction settled in RIG-9774, option A: the parent thread
owns `AssignmentLineage`, and a worker reaches it over RPC. This record
supersedes the in-process designs of RigelBuild/oh-my-pi PRs #241 (RIG-8517)
and #243 (RIG-8832). Neither PR has merged; neither design is on the
`rigel-release` base.

## Resolved decisions

### R1 — "Parent thread" means the main thread

RIG-9774 option A says the parent thread owns lineage. This record reads that
as **the main thread**, for every assignment, including a nested child whose
parent runs on a worker.

- The main thread already owns `AgentRegistry`, `AgentLifecycleManager` and
  `IrcBus`, and every worker already holds a link to it. A worker has no link
  to another worker.
- A parked agent can be revived on a different worker than the one that
  spawned it (`revivePersistedPlaced`). Lineage held on the spawning parent's
  thread would then need worker-to-worker routing.
- Nested subagents spawned from a worker run on that same worker
  (`subagentWorkerPool` returns `undefined` when `!isMainThread()`), so the
  only cross-thread pair today is main parent and worker child. Main is the
  parent thread for that pair.

### R2 — A dead worker terminally disposes every instance homed on it

When a worker exits, the advisory hub treats each session instance homed on it
as disposed, not parked. Assignments owned by those instances end, their
children are orphaned, and their check timers, retained snapshots and
subscriptions are released. A later revival of the same agent attaches a fresh
instance with no assignment; the next follow-up turn mints a new one.

Parking was the alternative. It loses because a crash bypasses
`AgentLifecycleManager.park`, so nothing later calls the parked-expiry path for
that session, and its lineage entry would never be freed. The registry's own
crash path also never claims the session survived: it detaches the ref and
marks it `parked` (on-disk transcript) or `aborted`.

### R3 — `TaskAdvisory` gains `contractVersion: 1`, and nothing else changes

The extension contract of #241 and #243 is kept as is: synchronous
`current()` and `children()`, `subscribe`, fire-and-forget `signal()`,
`remind()` returning `Promise<AdvisoryReceipt>`, and a `reply` event that
carries `state: AdvisoryState`. The consumer design for RIG-9798 asked for one
addition: `readonly contractVersion: 1`, so that an extension gates on the
contract and not on an oh-my-pi version pin. This record adds it.

The consumer also asked for fail-closed behaviour, and this design gives it:

- A child that has no running assignment, or whose worker died, is absent
  from `children()`. `remind` on its ref returns `unavailable`.
- A child whose messaging is off shows `messaging: { status: "unsupported" }`.
- `ctx.taskAdvisory` is `undefined` only when the session has no advisory
  attachment. It is never present and empty.

## Direction

- **Product fit.** RIG-9802 asks that the task advisory of #241/#243 keep
  working when `task.workerThreads > 0` places subagents on worker threads
  (upstream `d14c75a5`, merged into `rigel-release`). The extension-facing
  `TaskAdvisory` contract from #241/#243 does not change; only where its
  state lives does.
- **Scope.** One repository, RigelBuild/oh-my-pi, package
  `packages/coding-agent` plus the `IrcMessage` type in `packages/tui`. No
  other repository changes. Extensions that consume `ctx.taskAdvisory` need no
  change.
- **Prior art.**
  - [Ray object ownership](https://docs.ray.io/en/master/core/fault-tolerance/objects.html):
    the process that creates an object owns its metadata, and objects fate-share
    with their owner when it dies. Borrowed: one owner (main) holds all lineage
    metadata; state tied to a dead worker is dropped, not recovered.
  - [Temporal activity heartbeats](https://docs.temporal.io/develop/typescript/activities/timeouts):
    the worker running an activity reports progress to the server, and the
    server owns timeouts. Borrowed: the child's home thread reports progress
    events; main owns the check timers and derives the snapshot.
  - [Erlang process aliases and monitors](https://www.erlang.org/docs/25/reference_manual/processes.html):
    a message sent through a deactivated alias is dropped before it reaches the
    queue, and a `down` signal is dropped if its monitor is no longer active.
    Borrowed: a bound message carries the identity it was minted for, and the
    owner rejects it at every consumption point once that identity is stale.
- **Lifetime.** Long-lived. Worker mode is the upstream default direction for
  subagents, so this is the shape the advisory keeps. Design effort is sized
  to a core runtime contract: exact RPC table, exact cleanup rules, and tests
  that run against a real worker.
- **Simpler option.** Mirror the lineage into every worker and sync it with
  notifications. Rejected: lineage checks must be atomic with delivery, and a
  mirror that lags a mutation lets a stale reminder or reply through. One
  owner with synchronous worker-to-main reads has no consistency window.

## Problem / Intent

PR #241 keeps lineage, reminder timers, admission state and snapshots in
module-level singletons and `WeakMap<AgentSession, …>` tables. Under worker
mode each thread loads its own copy of those modules, the main-thread bus
cannot find a worker session in its `WeakMap`, symbol markers are lost in
structured clone, and redaction reads an obfuscator the main-side mirror does
not have. The intent is to move all advisory authority to one main-thread hub
that workers reach over the existing RPC link, so the advisory behaves the same
with worker mode on or off.

## Approach

### Shape: one main-thread hub, thin home-thread facades

```text
 worker wN (child home)                       main thread (authority)
 ┌─────────────────────────┐   callSync     ┌──────────────────────────────┐
 │ AgentSession (child)    │ ─────────────▶ │ AdvisoryHub                  │
 │  ├ AdvisoryAttachment   │   notify       │  ├ AssignmentLineage         │
 │  │  attach/detach/events│ ─────────────▶ │  ├ attach table (agent→inst) │
 │  └ TaskAdvisory facade  │   call         │  ├ per-assignment sources    │
 │     remind/reply/…      │ ─────────────▶ │  ├ check watches + timers    │
 │ IrcBridge / convertToLlm│                │  └ subscriber fan-out        │
 │  validate (batched)     │ ◀───────────── │ IrcBus.deliverBound          │
 └─────────────────────────┘ deliverIrcMsg  └──────────────────────────────┘
```

- **`AdvisoryHub`** (new, main only, `src/task/advisory-hub.ts`) owns one
  `AssignmentLineage`, the attach table, per-assignment snapshot sources, check
  watches and timers, and event fan-out. It replaces the per-session
  `AdvisoryRuntime` of `task-advisory-runtime.ts` and the module singleton
  `assignmentLineage`.
- **`AdvisoryHubApi`** (new interface) is what every thread calls.
  `AdvisoryHub.global()` returns the hub on main and a `RemoteAdvisoryHub`
  proxy on a worker, installed by a new `advisoryThreadService`. This is the
  same pattern as `IrcBus.global()` / `IrcBus.installGlobal()` and
  `RemoteIrcBus` in `thread/services/agents-remote.ts`.
- **Home-thread pieces** stay where the session lives: `AdvisoryAttachment`
  (attach, detach and event forwarding for one local `AgentSession`) and the
  `TaskAdvisory` facade handed to extensions. Both are thin: they read local
  session state the hub cannot see (current session id, obfuscator, events)
  and call the hub.

Why a new abstraction: the advisory needs one owner for state that spans two
sessions on possibly different threads (parent and child). No existing service
holds per-assignment state. `IrcBus` owns delivery, not identity, and putting
timers and lineage in it would mix two concerns. The hub reuses the bus for
delivery and reuses `AssignmentLineage`, `AdvisorySessionAdapter`,
`AdvisorySnapshotSource` and `parseReportInput` from #241/#243 unchanged in
logic.

### Identity across threads

- **Instance identity.** Each attach mints `instanceId = crypto.randomUUID()`
  on the home thread, as #241's `resetAdvisoryLineage` does. A session reset
  (new session, switch, branch) detaches the old instance and attaches a new
  one.
- **Attach table.** `AdvisoryHub.attach(sessionId, instanceId, agentId)`
  records `{ sessionId, instanceId, agentId, home }`. The hub resolves agent
  ids to session ids from this table only. It never reads
  `ref.session.sessionManager.getSessionId()` on main, because for a
  worker-homed session that is `RemoteAgentSession`'s mirrored state, which
  can lag a worker-side session switch.
- **Home stamping.** `home` is never taken from the payload. The main-side
  serve function of `advisoryThreadService` closes over its `WorkerPeer` and
  passes `peer.threadId`; local calls on main pass `"main"`. A worker cannot
  attach an instance to another thread.
- **Session ids on the home thread.** The facade and the attachment read
  `session.sessionManager.getSessionId()` locally, where it is authoritative.

### RPC rules

- Worker to main only. Main never makes a synchronous call to a worker
  (`RpcLink.callSync` is worker-to-main by construction in `thread/rpc.ts`).
- **`callSync`** for every lineage mutation and every read whose result the
  caller uses next: `attach`, `detach`, `navigate`, `branch`, `mint`,
  `mintFollowUp`, `wake`, `terminal`, `getAssignment`, `current`, `children`,
  `validate`, `subscribe`. Ordering is then identical to the in-thread code.
- **`notify`** (ordered, fire-and-forget) for progress: `publishEvent` and
  `signal`. A notify is ordered before every later call on the same link, so a
  `mint` (sync) followed by events from the new run reaches main in that order.
- **`call`** (async) for operations that deliver a message: `remind` and
  `sendBound`. Main awaits the recipient's `deliverIrcMessage`, which may be a
  worker command; the recipient's validation `callSync` back to main is served
  while main awaits, so there is no deadlock.

### Snapshots: home threads report events, main derives

The child's home thread maps its own session events to #241's
`AdvisoryEvent` shape and forwards them to the hub with `publishEvent`. Each
timed event carries `at = performance.timeOrigin + performance.now()`, an
epoch-based monotonic clock comparable across threads. #241's
`AdvisorySessionAdapter` class becomes the pure function `advisoryEventOf`,
run on the home thread. The hub keeps one `AdvisorySnapshotSource` per
assignment and projects `wallMs` and `unblockedMs` at read time with the same
clock. Unblocked time advances only while no blocking interval is open,
exactly as `AdvisorySnapshotSource` does today.

- The hub creates a new source when it mints an assignment, so counters reset
  per assignment without the home thread knowing about the mint. #241 reset
  its adapter when it noticed a new assignment id on the next event. The
  `ready` event is sent on every `agent_start`, and the source keeps the first
  one after the mint (`#readyAt ??= at`), so a follow-up turn starts its own
  clock.
- Events for a session with no running assignment are dropped on main.
- The publisher does not use `SessionPublisher`'s mirror: that forwards events
  only while main watches the session.
- **Parked children.** On `parkAgent`, the hub records `parkedAt` and keeps the
  last source. Reads project with `now = parkedAt` and `phase: "paused"`. A
  resume or release clears it. This is #241's "retains a paused snapshot"
  behaviour without a per-parent copy.

### Admission and stale-message rejection

- **Every bound message is minted by the hub.** `IrcBus.send` rejects a
  message that carries `advisoryBinding` (`outcome: "failed"`). The hub builds
  the binding from its own lineage for `remind` (parent to child), check
  follow-ups, and `sendBound` (child to parent). A worker never constructs a
  binding.
- **Admission runs on main** inside the bus delivery, through hooks the hub
  passes to a new main-only `IrcBus.deliverBound`. Watches, digests,
  duplicate detection, voluntary-report suspension (#243) and rollback are
  keyed by `assignmentId:requestId` and by message id in the hub. Nothing is
  looked up through a `WeakMap<AgentSession>`.
- **No admitted marker.** #241 set a symbol on the message object after
  admission so later checks could relax the phase test. Every bound message
  reaching a recipient queue has passed admission, so consumption checks always
  run in admitted mode, and the symbol is deleted. This removes the only
  advisory state that structured clone dropped.
- **Consumption points stay where they are** (the `IrcBridge` deliver, drain
  and queue paths and `convertToLlm`, all on the recipient's home thread). Each
  point collects every bound record it holds and makes **one** batched
  `AdvisoryHub.validate(checks)` call. The live metadata symbol
  `advisoryMessage` stays: it is local to the recipient thread and is never
  cloned again.

### Bound delivery and redaction

- **Redact on the sender's home thread.** The facade obfuscates `content` with
  the sending session's own `obfuscator` before the call crosses. #241
  redacted in `IrcBus.send` on main, where a worker sender's
  `RemoteAgentSession` has no obfuscator, so a worker child's secrets would have
  reached the parent in clear. The bus no longer redacts.
- **Caps hold on main.** The hub re-checks every byte limit after redaction:
  4096 UTF-8 bytes for the body, 128 for each id.
- **Delivery reaches any home.** `IrcBus.deliverBound` calls
  `session.deliverIrcMessage(message)`. For a worker-homed recipient that is
  `RemoteAgentSession.deliverIrcMessage`, which commands the home worker; the
  waiter path resolves a `bus.wait` parked on main and returns the message over
  the link.
- **No buffering.** A failed bound delivery rolls admission back and returns
  `unavailable`; it is never enqueued for a later `wait` or `inbox`.

### Worker exit

`advisoryThreadService.onWorkerExit(peer)` runs after
`agentsThreadService.onWorkerExit`, because `THREAD_SERVICES` lists the
advisory service directly after the agents service and `SubagentWorker` runs
exit hooks in list order. It:

1. releases every hub subscription the worker opened (`PeerCleanups`), and
2. calls `hub.disposeHome(peer.threadId)`, which terminally disposes each
   attached instance whose `home` is that thread (R2): own assignment ended,
   children orphaned, watches and timers cleared, parked snapshot dropped.

A main-thread parent then sees the child disappear from `children()`, and
`remind` on its old ref returns `unavailable`.

### Alternatives considered

- **Lineage on the parent session's own thread.** Rejected in R1:
  worker-to-worker routing does not exist, and revival can move a session.
- **Keep #241's runtime and add RPC calls where it breaks.** Rejected: its state
  is keyed by local `AgentSession` objects in several `WeakMap`s, so every
  table would need a cross-thread twin. One hub keyed by ids is smaller.
- **Child computes the snapshot and publishes it.** Rejected: the child would
  need to learn about each mint to reset counters, which is an extra
  main-to-worker notification. Forwarding raw events lets main reset on mint
  with no new message direction.

## Global Constraints

- Base: `rigel-release` of RigelBuild/oh-my-pi at or after `d5719bf9980e`,
  which contains upstream `d14c75a5`. The work is one PR off that base that
  carries the #241 and #243 behaviour in the new shape. #241 and #243 close
  unmerged when it merges.
- No new dependencies. Bun test (`bun:test`) for every test.
- Extension contract: `TaskAdvisory` and every type in
  `src/extensibility/extensions/task-advisory.ts` stay as in #241 + #243, plus
  `readonly contractVersion: 1` (R3).
- Limits stay as #241/#243 set them: 4096 UTF-8 bytes per bound body, 128 per
  id (`requestId`, `assignmentId`, every `AssignmentRef` field), 240 characters
  per report string, 3 `tried` entries, 64 open checks per assignment, a check
  tick every 15 s, and one follow-up at 300 s.
- Clock: every cross-thread timestamp is
  `performance.timeOrigin + performance.now()`, named `advisoryNow()` and
  exported from `src/task/advisory-hub.ts`. `Date.now()` is never used for
  advisory intervals.
- Thread rules: worker-to-main only for `callSync`; main never calls a worker
  synchronously; `home` is stamped by the main-side serve function, never read
  from a payload.
- `IrcBus.send` refuses a message that carries `advisoryBinding`. Only
  `IrcBus.deliverBound`, called by the hub, delivers bound messages.
- Bound messages are never buffered and never relayed to the main UI.

## Plan

### Task 1 — Contract and pure units

Bring the thread-agnostic parts of #241 and #243 onto the base with their
tests. Logic does not change except where named.

- `src/extensibility/extensions/task-advisory.ts`: the #243 types, plus
  `readonly contractVersion: 1` on `TaskAdvisory`.
- `src/task/assignment-lineage.ts`: `AssignmentLineage`, `AssignmentBinding`,
  `AdvisoryTransportBinding`, `AdvisoryBindingFailure` from #241. Delete the
  module singleton `export const assignmentLineage`; the hub owns the only
  instance.
- `src/task/advisory-snapshot-source.ts`: `AdvisorySnapshotSource` and
  `AdvisoryEvent` from #241.
- `src/task/advisory-session-adapter.ts`: replace the class with
  `advisoryEventOf`. It returns the mapped event, or `undefined` for events the
  advisory ignores. `ready` is emitted on every `agent_start`.
- `src/task/advisory-report.ts`: `parseReportInput` from #243.
- `packages/tui/src/tools/irc.ts`: `IrcMessage.assignmentId`, `checkId`,
  `advisoryBinding`, and `IrcDeliveryReceipt` outcome `"duplicate"`, from #241.
- The `message_persisted` session event and its RPC exclusions from #241.

Interfaces:

```ts
// src/task/advisory-session-adapter.ts
export function advisoryEventOf(event: AgentSessionEvent, at: number): AdvisoryEvent | undefined;

// src/extensibility/extensions/task-advisory.ts
export interface TaskAdvisory {
	readonly contractVersion: 1;
	current(): AdvisorySnapshot | undefined;
	children(): readonly AdvisorySnapshot[];
	subscribe(listener: (event: TaskAdvisoryEvent) => void): () => void;
	signal(signal: LocalSignal, count: number): void;
	remind(ref: AssignmentRef, requestId: string, content: string): Promise<AdvisoryReceipt>;
}
```

Tests: port `test/registry/assignment-lineage.test.ts`,
`test/task/advisory-snapshot.test.ts`, `test/task/advisory-report.test.ts`.
Rewrite `test/task/advisory-session-adapter.test.ts` against
`advisoryEventOf` with the same cases (overlapping tools, retry, compaction,
repeated readiness, usage counted once by id).

### Task 2 — `AdvisoryHub` on main and bound bus delivery

New `src/task/advisory-hub.ts`. It holds what #241's `AdvisoryRuntime` held,
keyed by ids instead of sessions:

- one `AssignmentLineage`;
- the attach table `sessionId → { instanceId, agentId, home }` and the reverse
  index `agentId → sessionId`;
- `sources: Map<assignmentId, { source: AdvisorySnapshotSource; parkedAt?: number }>`;
- `watches: Map<"assignmentId:requestId", Watch>` with #241's fields
  (`timer`, `content` digest, `reply`, `replyDelivered`, `expired`,
  `pending`, `followUpSent`) and `issuedIds`;
- `replyStates: Map<messageId, AdvisoryState>` and
  `voluntaryWatchKeys: Map<messageId, string[]>` (#243's `WeakMap`s, keyed by
  the bus message id; entries are removed on receive or rollback);
- `listeners: Map<sessionId, Set<(event: TaskAdvisoryEvent) => void>>`.

Lineage listeners (`onPark`, `onResumeOrRelease`, `onTerminal`) clear watches,
`issuedIds`, sources and parked marks the way #241's runtime did. `children`
and `current` project from `sources` with `advisoryNow()`, or with `parkedAt`
for a parked child (phase `"paused"`). `localCoverage` stays
`detector-unregistered` for both detectors, as in #241.

`src/irc/bus.ts`:

- `send` returns `{ to, outcome: "failed", error: "Advisory assignment is unavailable." }`
  for any message with `advisoryBinding`, before delivery. It stops redacting.
- New main-only `deliverBound`. It runs #241's `#deliver` path with
  `noBuffer` and `suppressRelay`, re-runs `validate` after any revive await,
  and calls the hooks at the points #241/#243 called `admitTaskAdvisory`,
  `receiveTaskAdvisory` and `rollbackTaskAdvisory`. The receive hook runs only
  after `deliverIrcMessage` resolved (#243 order).
- `src/irc/advisory.ts` keeps `advisoryMessage` and drops `admitted`,
  `markAdvisoryAdmitted` and the `AgentRegistry` lookup.

Interfaces:

```ts
// src/task/advisory-hub.ts
export function advisoryNow(): number;

export interface AdvisoryAttach {
	sessionId: string;
	instanceId: string;
	agentId: string | undefined;
}

export interface AdvisoryCheck {
	fromAgentId: string;
	toAgentId: string;
	binding: AdvisoryTransportBinding;
}

export interface AdvisoryMint {
	childSessionId: string;
	parentSessionId: string;
	parentBranchId?: string;
	messaging: AdvisoryMessagingSupport;
}

export interface AdvisoryRemind {
	parentSessionId: string;
	ref: AssignmentRef;
	requestId: string;
	/** Already redacted on the parent's home thread. */
	content: string;
}

export interface AdvisoryReply {
	childSessionId: string;
	fromAgentId: string;
	toAgentId: string;
	assignmentId: string;
	checkId?: string;
	/** Already redacted on the child's home thread. */
	body: string;
}

/** What every thread calls; the hub on main, a proxy on a worker. */
export interface AdvisoryHubApi {
	attach(input: AdvisoryAttach): void;
	detach(sessionId: string, instanceId: string): void;
	branch(sessionId: string): string;
	navigate(sessionId: string): void;
	mint(input: AdvisoryMint): AssignmentRef;
	/** Re-mint for a follow-up turn; resolves the parent through the attach table. */
	mintFollowUp(childSessionId: string, parentAgentId: string): AssignmentRef | undefined;
	wake(sessionId: string): void;
	terminal(sessionId: string, assignmentId: string): void;
	getAssignment(sessionId: string): AssignmentBinding | undefined;
	current(sessionId: string): AdvisorySnapshot | undefined;
	children(parentSessionId: string): AdvisorySnapshot[];
	publishEvent(sessionId: string, instanceId: string, event: AdvisoryEvent): void;
	signal(sessionId: string, signal: LocalSignal, count: number): void;
	subscribe(sessionId: string, listener: (event: TaskAdvisoryEvent) => void): () => void;
	remind(input: AdvisoryRemind): Promise<AdvisoryReceipt>;
	sendBound(input: AdvisoryReply): Promise<AdvisoryReceipt>;
	/** One result per check, in order: true when the binding is still current. */
	validate(checks: readonly AdvisoryCheck[]): boolean[];
}

export class AdvisoryHub implements AdvisoryHubApi {
	static global(): AdvisoryHubApi;
	static installGlobal(api: AdvisoryHubApi): void;
	static resetGlobalForTests(): void;
	/** The main-thread hub; throws when called on a worker. */
	static main(): AdvisoryHub;
	/** Main-side serve functions call this with the worker's thread id. */
	attachFrom(home: ThreadId, input: AdvisoryAttach): void;
	/** Main only; called by `AgentLifecycleManager.park`. */
	parkSession(sessionId: string): void;
	/** Main only; called by `AgentLifecycleManager.release` for a parked agent. */
	expireParked(sessionId: string): void;
	/** Main only; terminal disposal of every instance homed on `home` (R2). */
	disposeHome(home: ThreadId): void;
	// …plus every AdvisoryHubApi method; `attach` is `attachFrom("main", input)`.
}

// src/irc/bus.ts
export interface BoundDeliveryHooks {
	validate(): AdvisoryBindingFailure | undefined;
	admit(message: IrcMessage): "accept" | "duplicate" | "reject";
	received(message: IrcMessage): void;
	rollback(message: IrcMessage): void;
}
// on class IrcBus (main only, not on IrcBusApi):
deliverBound(msg: Omit<IrcMessage, "id" | "ts">, hooks: BoundDeliveryHooks): Promise<IrcDeliveryReceipt>;
```

Tests (`test/task/advisory-hub.test.ts`, in-thread, fake timers): port the
cases of `advisory-timing.test.ts` and `task-advisory-runtime.test.ts` (from
PRs #241/#243) against `AdvisoryHub`; add that `IrcBus.send` with a binding fails
and is not buffered; that a hub-minted reply with a stale instance is
rejected; that the cap is checked after redaction; that
`disposeHome("w1")` clears watches, timers (`vi.getTimerCount() === 0`),
sources and children only for instances homed on `w1`.

### Task 3 — Home-thread wiring and consumption checks

- New `src/task/advisory-attachment.ts`: `AdvisoryAttachment` owns one local
  `AgentSession`'s attach/detach and event forwarding, and builds the
  `TaskAdvisory` facade. `AgentSession.resetAdvisoryLineage()` and
  `disposeAdvisoryLineage()` from #241 call it at the same call sites (session
  init in `sdk.ts`, new session, switch, both branch paths, dispose).
  `navigate` call sites stay as in #241 and go through `AdvisoryHub.global()`.
- The facade redacts `remind` content with `session.obfuscator` before calling
  the hub, and checks the 4096-byte cap locally and again on main.
- `src/irc/messaging.ts` `executeSend`: the bound branch validates and parses
  as #243 does, redacts with the sender's obfuscator, then calls
  `AdvisoryHub.global().sendBound(...)`. The write-tool and `agent://`
  parameter plumbing from #241 is kept.
- `src/task/executor.ts`, `src/task/structured-subagent.ts`: every
  `assignmentLineage.*` call from #241 becomes `AdvisoryHub.global().*`.
  `runSubagentFollowUpTurn` calls `mintFollowUp(childSessionId, ref.parentId)`
  in place of reading the parent's session id through the registry.
- `src/registry/agent-lifecycle.ts`: `park` calls
  `AdvisoryHub.main().parkSession(sessionId)` and `release` of a parked agent
  calls `AdvisoryHub.main().expireParked(sessionId)`, at #241's call sites.
  The lifecycle manager runs only on main.
- `src/extensibility/extensions/runner.ts`: `ctx.taskAdvisory` reads
  `taskAdvisoryFor(sessionId)`, a lookup in this thread's attachment map.
- `src/session/irc-bridge.ts` and `src/session/messages.ts`: replace each
  per-record `advisoryIsCurrent` with one `validate` call per drain, queue,
  deliver or `convertToLlm` pass over the bound records it holds. Drop the
  advisory scan cache only if the batched form makes it unused.

Interfaces:

```ts
// src/task/advisory-attachment.ts
export class AdvisoryAttachment {
	constructor(session: AgentSession);
	readonly advisory: TaskAdvisory;
	/** Detach the current instance (if any) and attach a fresh one. */
	reset(): void;
	dispose(): void;
}
export function taskAdvisoryFor(sessionId: string): TaskAdvisory | undefined;

// src/irc/advisory.ts
export const advisoryMessage: unique symbol;
/** Batched staleness check for the bound records a consumption point holds. */
export function currentAdvisories(messages: readonly IrcMessage[]): boolean[];
```

Tests: port `advisory-delivery-e2e.test.ts`,
`assignment-lineage-integration.test.ts`, `advisory-parked-snapshot.test.ts`,
`advisory-session-real.test.ts`, `advisory-tagged-write.test.ts`,
`irc-bridge-relay.test.ts` and the `test/tools/irc.test.ts` additions. These
run in-thread and prove parity with #241/#243.

### Task 4 — `advisoryThreadService`

- New `src/thread/services/advisory-protocol.ts`: `ADVISORY_HUB_KIND =
  "advisory-hub"` and `AdvisoryHubProtocol`, one `RpcMethod` per
  `AdvisoryHubApi` method with the same argument tuple. `subscribe` returns a
  `WireUnsubscribe`.
- New `src/thread/services/advisory-remote.ts`: `RemoteAdvisoryHub implements
  AdvisoryHubApi`. `publishEvent` and `signal` use `notify`; `remind` and
  `sendBound` use `call` (the API returns a promise); every other method uses
  `callSync`. `subscribe` uses one `ForwardedListeners`-style forwarding
  subscription per session id.
- New `src/thread/services/advisory.ts`: `advisoryThreadService`.
  `installMain` exports the hub with a per-peer serve table whose `attach`
  calls `hub.attachFrom(peer.threadId, input)` and whose `subscribe` is tracked
  in a `PeerCleanups`. `globals` hands the hub. `installWorker` imports the
  class. `boot` calls `AdvisoryHub.installGlobal(remote)`. `onWorkerExit`
  releases the peer's cleanups, then calls `hub.disposeHome(peer.threadId)`.
- `src/thread/services/index.ts`: insert `advisoryThreadService` directly
  after `agentsThreadService`.

Interfaces:

```ts
// src/thread/services/advisory-protocol.ts
export const ADVISORY_HUB_KIND = "advisory-hub";
export type AdvisoryHubProtocol = {
	attach: RpcMethod<[input: AdvisoryAttach], void>;
	detach: RpcMethod<[sessionId: string, instanceId: string], void>;
	branch: RpcMethod<[sessionId: string], string>;
	navigate: RpcMethod<[sessionId: string], void>;
	mint: RpcMethod<[input: AdvisoryMint], AssignmentRef>;
	mintFollowUp: RpcMethod<[childSessionId: string, parentAgentId: string], AssignmentRef | undefined>;
	wake: RpcMethod<[sessionId: string], void>;
	terminal: RpcMethod<[sessionId: string, assignmentId: string], void>;
	getAssignment: RpcMethod<[sessionId: string], AssignmentBinding | undefined>;
	current: RpcMethod<[sessionId: string], AdvisorySnapshot | undefined>;
	children: RpcMethod<[parentSessionId: string], AdvisorySnapshot[]>;
	publishEvent: RpcMethod<[sessionId: string, instanceId: string, event: AdvisoryEvent], void>;
	signal: RpcMethod<[sessionId: string, signal: LocalSignal, count: number], void>;
	subscribe: RpcMethod<[sessionId: string, listener: WireCallback<[TaskAdvisoryEvent]>], WireUnsubscribe>;
	remind: RpcMethod<[input: AdvisoryRemind], AdvisoryReceipt>;
	sendBound: RpcMethod<[input: AdvisoryReply], AdvisoryReceipt>;
	validate: RpcMethod<[checks: readonly AdvisoryCheck[]], boolean[]>;
};

// src/thread/services/advisory.ts
export const advisoryThreadService: ThreadService;
```

Tests: new `test/thread/advisory-bridge.test.ts` with a fixture worker
`test/fixtures/advisory-bridge-worker.ts`, in the style of
`agents-bridge.test.ts` (services `identity`, `agents`, `advisory`, and a
probe). Cases: a worker `attach` is stamped `home: w<N>` even when the payload
names another thread; `callSync` reads see a mutation made just before them;
`publishEvent` then `current` returns the event's effect; worker exit drops the
worker's subscriptions and runs `disposeHome` exactly once.

### Task 5 — Worker-mode end-to-end

Extend `test/thread/executor-routing.test.ts` (real pooled worker,
`setSubagentWorkerPoolForTests`, scripted model server). Main-thread parent,
worker-homed child:

1. `children()` on the parent shows the worker child's snapshot with
   `unblockedMs` below `wallMs` while a tool runs.
2. `remind` is delivered to the worker child; the child's bound reply arrives
   as one `reply` event with its `state`; nothing is relayed to the main UI.
3. A reply after the parent navigates (`navigateTree`) is dropped at
   consumption and never reaches `convertToLlm` output.
4. A secret known only to the worker child's obfuscator never appears in the
   parent's inbox.
5. `worker.terminate("worker crashed: boom")` mid-run: the child leaves
   `children()`, `remind` on its old ref returns `unavailable`, and no hub
   timer stays armed.

### Task 6 — Changelog and supersession

- `packages/coding-agent/CHANGELOG.md` under `[Unreleased]`: the #241 and #243
  entries, plus "Task advisory works for subagents on worker threads."
- Close #241 and #243 with a link to the new PR when it merges.

## Tasks

- [ ] T1 Contract and pure units (`contractVersion: 1`, lineage without
      singleton, `advisoryEventOf`, report parser, `IrcMessage` fields);
      ported unit tests.
- [ ] T2 `AdvisoryHub` + `IrcBus.deliverBound`; `send` refuses bindings;
      hub unit tests with fake timers.
- [ ] T3 `AdvisoryAttachment`, facade, executor/lifecycle/messaging wiring,
      batched `validate` at every consumption point; in-thread parity tests.
- [ ] T4 `advisoryThreadService` + `RemoteAdvisoryHub`, placed after
      `agentsThreadService`; worker bridge tests.
- [ ] T5 Worker-mode end-to-end cases 1–5 in `executor-routing.test.ts`.
- [ ] T6 Changelog; close #241 and #243.
