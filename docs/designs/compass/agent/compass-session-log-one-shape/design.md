# Session Log: one event shape end to end

> Freezes on merge; later changes supersede by citation, never rewrite.

Issues: RIG-5945 (shape), RIG-5941 (empty log for an idle agent), RIG-5943
(subagent logs). Parent record:
[compass-agent session persistence](../compass-agent-session-persistence/design.md#one-canonical-artifact-the-session-log-unified-with-the-trace-r1)
(DL-088: the session-entry log is the one canonical artifact; RIG-1580 was to
collapse the two streams). DL-636 supersedes DL-287 in
[per-agent overrides](../compass-per-agent-overrides/design.md#ui-seam-subagent-work-nests-under-the-manager-in-the-session-log).

<!-- LENGTH §Plan: eleven tasks over two stacks need exact Interfaces -->

## Open Questions

Each question has a recommendation, and the Plan assumes it.

1. **Q1 Which shape.** Option 1: OMP session-file entries everywhere. Option
   2: typed `SessionEvent` everywhere, with a second durable store (see
   Alternatives considered). **Recommendation: option 1.** The Q1 DL then
   supersedes DL-318 and DL-323; DL-322's meanings carry over (D3).
2. **Q2 History transport (depends on Q1).** A paged RPC plus a bounded
   backfill on subscribe, or all history on the stream.
   **Recommendation: the paged RPC plus backfill (D2).**
3. **Q3 History depth (depends on Q1).** The current file view, or the raw
   archive with `superseded` segments. **Recommendation: the current file
   view (D2).**
4. **Q4 Subagent identity across a resume (depends on Q1).** OMP avoids
   reusing a task id only by scanning the artifacts directory
   (`AgentOutputManager`, `task/output-manager.ts`). A resumed container gets
   only the root file, so a new subagent can reuse an old task id. A key of
   (root, task id) then merges two runs into one child log. Options:
   - (a) Key by (root, root lifetime epoch, task id). `BindLifetime` bumps the
     epoch.
   - (b) Key by (root, `parentToolCallId`, task id). This is unique per spawn,
     but it splits a subagent that OMP resumes within one lifetime.
   - (c) The Runner writes marker files so OMP's scan reserves old ids. This
     couples Compass to OMP's scan rules.

   **Recommendation: (a).** It matches OMP's identity, one file per lifetime
   and task id, and a child needs no seq rebase. B2 first confirms that a
   resume hands out an old task name again.
5. **Q5 OMP delivery.** compass-agent uses npm OMP 18.0.11, not the RigelBuild
   fork. **Recommendation: an upstream change and an npm bump.** Use a fork
   patch under the [forks reversal](../compass-forks-reversal/design.md) rules
   only if upstream refuses.
6. **Q6 Compass data in the session file (depends on Q1).** OMP `custom`
   entries, or a side table. **Recommendation: custom entries (D3).**
7. **Q7 A checkpoint on the live stream (depends on Q2).** A checkpoint row is
   the whole file, up to 16 MiB. Relayed as is, it loads all history into a
   paged UI, and every older position the UI holds falls out of the history
   view. Options:
   - (a) Never relay a checkpoint. Send `resync_required`, and the UI
     re-lists the newest page. A page request below the latest checkpoint
     returns `FailedPrecondition` and gets the same handling.
   - (b) Relay it in 1 MiB chunks, and the UI replaces its lines.

   **Recommendation: (a).** Live then always matches a reopen. The cost is
   one page read per checkpoint and a lost scroll position.
8. **Q8 Where a subagent click lives.** Matt wrote "the board or the log"
   (RIG-5943). The relay of his ruling says "the tree or the log", and the
   sidebar tree has no subagent rows yet. **Recommendation: board chips and log
   rows open the child log in this record. Tree rows follow the RIG-5946
   layout and use the same open call.**
9. **Q9 What DL-287 keeps.** Matt did not restate DL-287's no per-subagent
   channel rule or its two-level trace. **Recommendation: claim the
   no-channel rule as a new DL once Matt confirms it. Show grandchildren
   nested under their parent, because the tee captures them anyway.**

## Problem / Intent

The Session Log pane is empty for an idle agent (RIG-5941). The pane reads
only `SubscribeAgentSession`, and `sessionTail` (`go/server/sessiontail.go`)
says: "Subscribers join at the live head … there is no replay." Stored history
cannot fill the gap (RIG-5945). Postgres
`agent_session_transcript_entries.entry_json` and the S3 segments hold raw OMP
session-file lines. Live sends `EventMapper` `SessionEvent`s, and no mapping
exists between the two. After a Server redeploy the UI also has no session id
for an idle agent, because its account-to-session map comes from in-memory
`agentSessionStatus` events. Subagent sessions never reach the store: OMP
opens them with the default file storage at spawn and at both revive paths,
so the tee never sees them (RIG-5943).

Intent: one data model for live, history and archive; a log that opens for
any agent from durable state; and running subagents listed in their parent's
log, each with its own log.

## Approach

### Ruling: one shape for live, history and archive

Matt ruled on RIG-5945 (2026-10-19) that the data model is aligned end to end
(DL-635). Q1 is open. This record plans option 1, where the model is the OMP
session-file entry (`SessionEntry`, `session/session-entries.ts`). OMP commits
an entry whole, at `message_end`. Live also carries deltas for the message in
progress, one frame per text, thinking or tool-output chunk, as `EventMapper`
streams today. The Server never stores them, and the committed entry replaces
them. Session state (STARTING, WORKING, READY, STOPPED, ERRORED) and
`batch_pending` stay transient `AgentSessionFrame` fields: they have no entry,
and history never replays them. Injections and stream errors or aborts have no
OMP entry, so they become Compass custom entries (D3) and reach history too.

### D1 Wire shapes

```proto
// One stored transcript row, or a chunk of one. A checkpoint row is a whole
// session file; body then holds its lines from first_line on.
message SessionLogRow {
  uint64 entry_seq = 1;
  bool checkpoint = 2;
  uint32 first_line = 3;
  string body = 4; // newline-joined session-file lines, verbatim
}

message SessionLiveEvent {
  oneof kind {
    SessionLiveText text = 1;
    SessionLiveTool tool = 2;
  }
}
message SessionLiveText {
  uint64 live_seq = 1; // agent's message number, per process (A4)
  uint32 content_index = 2;
  bool thinking = 3;
  string delta = 4;
}
message SessionLiveTool {
  string tool_call_id = 1;
  string tool_name = 2;
  string output = 3; // latest partial text, capped at 4000 characters
}
```

A line's position is `(entry_seq, line)`. The Server splits rows on newlines,
as `splitSegmentLines` (`runnerhub/reconstruct.go`) does, and never parses a
line.

Deltas ride the loss-tolerable spine and rows ride the durable lane, so they
can arrive in either order. OMP has no message id that both carry: it mints
entry ids at persist time, and two assistant messages can share a `timestamp`
millisecond. So the agent numbers messages itself. `LiveSeq` (A4) starts a new
number at the first assistant `message_start` or `message_update` after the
last assistant `message_end`, and keeps the number of that last end. Text
deltas carry the current number as `live_seq`. OMP persists a message only
after it emits `message_end` (`#processAgentEvent`, `agent-session.ts`). So the
tee's stamp, `TranscriptEntry.ended_live_seq`, is never below the number of
the message a row holds, and always below the number of the message in
progress. The hub relays the stamp with the row and does not store it. The UI
keeps the highest stamp it has seen, and:

- drops a text delta whose `live_seq` is at or below it, and a tool delta
  whose `tool_call_id` has a committed `toolResult`;
- clears provisional text at or below it when a row arrives;
- clears all provisional rows on any state other than WORKING. This covers
  turns OMP never commits, such as classifier refusals.

OMP sends `message_update` to subscribers at once but queues `message_start`
and `message_end` (`#emitSessionEvent`), so a next message's first deltas can
arrive before the last `message_end`. They then carry the last number. A
stamp is read when the tee frames a row, after its local write, so it can run
ahead of rows in flight. Both errors are safe: a number can only be too low
and a stamp only too high, so the worst case is provisional text hidden until
its row lands, never text shown twice. The mark is per process, so the UI
resets it on STARTING and on every subscribe. A backfilled row has no stamp,
so a stale delta after a resubscribe shows until the next row or state change
clears it.

`AgentSessionFrame` reserves `event = 2` and adds `SessionLogRow row = 5`,
`SessionLiveEvent live = 6`, `SessionBatchPending batch_pending = 7`,
`bool resync_required = 8` and `uint64 ended_live_seq = 9` (set with `row`).
The internal `SessionFrame` reserves `typed_event = 2` and adds `live = 4` and
`batch_pending = 5`. `TranscriptEntry` adds `uint64 ended_live_seq = 4`.

### D2 History, the live boundary, and resubscribe

History is the current file. The tee's `writeFull` sends a checkpoint row that
replaces every earlier row, so history is the latest checkpoint row, then
later Postgres rows and `safety_valve` segments. This is the set
`SessionResumeSnapshot` (`store/agent_transcripts.go`) reads for a resume.
The `superseded` and `session_end` segments are never read
(`FlushSuperseded` keeps `session_end` rows in Postgres). So a normal session
reads no S3 object.

```proto
rpc ListSessionLog(ListSessionLogRequest) returns (ListSessionLogResponse);

message SessionLogPosition {
  uint64 entry_seq = 1;
  uint32 line = 2;
}
message ListSessionLogRequest {
  oneof target {
    string session_id = 1;
    string agent_account_id = 2; // the account's latest root session
    // 3: SubagentRef subagent, added in B3
  }
  SessionLogPosition before = 4; // unset = from the newest line
  uint32 limit = 5;              // lines; the server clamps to 500
}
message ListSessionLogResponse {
  string session_id = 1;
  repeated SessionLogRow rows = 2;    // oldest-first
  SessionLogPosition next_before = 3; // unset = no older lines
  uint64 head_entry_seq = 4;
}
```

A page holds at most `limit` lines and 1 MiB of body, and reads at most one
segment. Segment lines are stamped `min_entry_seq + i`, as
`ReconstructSessionBody` does. The page and `head_entry_seq` come from one
REPEATABLE READ transaction. Auth is `RequireAgentSessionSubscriber`. An
unknown target and a forbidden one both return NotFound.

`SubscribeAgentSessionRequest` adds `uint64 after_entry_seq = 2`. The handler
does these steps in order:

1. Register the tail subscriber. Live frames buffer from here.
2. Send the DL-310 registration ack, unchanged.
3. In one REPEATABLE READ transaction, read up to 256 Postgres rows above
   `after_entry_seq`. In the same transaction, check for a `safety_valve`
   segment or a checkpoint above it. Send the rows.
4. If rows remain, or that segment or checkpoint exists, send
   `resync_required` and end.
5. Relay live frames. Drop rows at or below max(`after_entry_seq`, the last
   row sent).

Step 1 comes before step 3, so each commit is in the read, the buffer, or
both, and step 5 drops the overlap. The UI lists first, then subscribes from
`head_entry_seq`. It pages older lines with `next_before`. When the stream
ends, for a redeploy or a full `sessionTail` buffer, the UI resubscribes from
its highest `entry_seq`. On `resync_required` it drops its lines and opens
again.

### D3 Compass data in the session file

The agent writes Compass data that has no OMP entry with
`SessionManager.appendCustomEntry`. A `CustomEntry` stays out of the model
context.

| customType | data | Written when |
|---|---|---|
| `compass.injection` | `{opKind, messageId, fromHandle, traceparent}` | where `#emitInjection` emits today (`agent.ts`) |
| `compass.session_error` | `{kind: "error" or "aborted", message, status?}` | where `EventMapper` emits `SessionError` today |
| `compass.subagent` | `{id, agent, description, status, parentToolCallId?}` | on each `task:subagent:lifecycle` event (B4) |

OMP persists most error turns, including terminal empty ones
(`persistTerminalEmptyErrorTurn`, `agent-session.ts`), but not classifier
refusals. So the fold draws the error banner only from
`compass.session_error`, and draws OMP error turns as plain assistant rows.
DL-322 holds: `error` writes the entry plus the ERRORED transition, and
`aborted` writes the entry only. `state`, `turn_sequence` and `batch_pending`
stay transient. `traceparent` keeps the DL-303 link.

### D4 The emitter

`EventMapper` (`packages/compass-agent/src/mapping.ts`) keeps its lifecycle
arms, so session state stays a transient frame. It emits one
`SessionLiveEvent` per text, thinking or tool-output chunk, stamped through
`LiveSeq` (D1). For an error it returns a note, which `agent.ts` writes as
`compass.session_error`. The plan, diff, notice and `SessionEvent` arms go.
The UI fold reads the same `details` from `toolResult` entries.

### D5 UI types and rendering

The UI imports OMP's `SessionEntry` type-only, through `@compass/client` and
the package's `./session/*` type export, so nothing reaches the bundle. The
fold switches exhaustively on entry `type` and message `role`, so an OMP bump
that adds a variant fails the typecheck. Tool `details` are `unknown`, so the
fold guards them at runtime. A line that does not parse renders as an
unknown-entry row.

The fold renders file order. OMP keeps abandoned branches in the file, and a
`branch_summary` renders as a notice where the branch changed. Unlike the TUI,
the log does not hide abandoned branches, because that needs a tree walk
across pages. Images are `blob:sha256:<hex>` references
([session blobs](../compass-agent-session-blobs/design.md) D1) and render as
placeholders.

### D6 Subagents: listed in the log, each with its own log

Matt ruled on RIG-5943 (2026-10-19) that subagents show in the Session Log
pane as in the OMP TUI, and that a click on one opens its current Session Log
(DL-636). Q8 and Q9 hold the open parts.

- **Capture.** B1 opens every subagent file with the parent's storage: at
  spawn, at a warm revive, and at a cold revive of a parked ref. So the tee
  sees all their writes. A revived turn emits lifecycle `started` again with
  its `sessionFile` (`attachIrcWakeTurnMonitor`, `runSubagentFollowUpTurn`,
  `task/executor.ts`). The tee routes a write by an exact path-to-id map
  filled from those `started` events (`sessionFile`, `id`), with one seq
  counter and one `LiveSeq` per subagent.
- **Keying (Q4).** `BindLifetime` bumps the root's `lifetime_epoch`.
  `EnsureSubagentSession` mints a child row keyed by (root, epoch, task id),
  copying `agent_account_id` and `tenant_id`, so
  `RequireAgentSessionSubscriber` authorizes it unchanged.
  `LatestSessionForAccount` skips child rows, for the log and the wake path.
- **Live.** The agent maps `task:subagent:event` deltas from the root
  `subagentEventBus` with `subagent_id` set. The hub relays them to the
  child's tail only.
- **List.** The root log lists subagents from `compass.subagent` entries,
  latest status per id, nested by id prefix.

### D7 Opening a log

The UI takes the session id from `accountSessions` when the agent has a live
status. Otherwise it calls `ListSessionLog` with `agent_account_id`, which
reads `LatestSessionForAccount` from Postgres, so the open survives a
redeploy. A subagent opens with the `subagent` target, which resolves the
newest epoch's child. `BoardSubagent.id` is the same task id, so a board chip
sends the same request.

## Alternatives considered

- **Option 2 (Q1).** Rejected: two durable stores of one session can drift,
  against DL-088. The mapper drops data by design
  (`OUTPUT_TEXT_LIMIT = 4_000`, no user messages), and the archive would need
  a Go backfill mapper.
- **History frames on the stream (Q2).** One RPC, but the stream then needs
  unbounded history at open or a second path for older pages.
- **All segment kinds (Q3).** Shows pre-prune tool output, but a deep scroll
  reads every `superseded` object.
- **A side table (Q6).** A second store with a second order to merge.
- **A hand-written entry mirror (D5).** No OMP type dependency, but a test of
  the mirror either fails on unmapped variants or passes every change.
- **A timestamp or OMP persistence key on deltas (D1).** Two messages can
  share a millisecond, and `responseId` is optional and provider-specific
  (`sessionMessagePersistenceKey`, `session/turn-persistence.ts`).

## Global Constraints

- **Spend freeze.** No new usage-billed resource. S3 reads touch only
  `safety_valve` segments, one object per page. No new metric series and no
  per-entry log line.
- **Store opacity.** The store and the hub split rows on newlines only.
- **Boundary.** No gap and no duplicate between history and live, by
  `entry_seq`.
- **Server-minted ids**, child ids included.
- **Skew.** An agent keeps its image until restart. One built before A4 sends
  only `typed_event`, which the new Server ignores, so its log shows committed
  entries without deltas until restart.
- **Acceptance (Matt).** On dev, after a Server redeploy, the UI opens the log
  of an agent idle for hours and shows its last turn (stack A). A running
  subagent shows in its parent's log and opens its own log (stack B).

## Plan

Stack A (RIG-5945, RIG-5941) is A1 to A6. Stack B (RIG-5943) is B1 upstream
(Q5), then B2 to B5 on the tip of stack A.

### A1 Proto (lane: compass-server)

Add the D1 and D2 messages and fields as written, without subagent fields.
`SessionEvent` stays until A6.

Interfaces: `ListSessionLog`; `SessionLogRow`, `SessionLogPosition`,
`SessionLiveEvent`; `AgentSessionFrame` fields 5 to 9;
`SubscribeAgentSessionRequest.after_entry_seq = 2`; internal `SessionFrame`
fields 4 and 5; `TranscriptEntry.ended_live_seq = 4`.

Tests: `buf lint` and generated code compile.

### A2 Store history reads (lane: compass-server)

Interfaces (`go/internal/store`):

- `func (s *Store) AppendTranscriptEntry(ctx context.Context, sessionID string, lifetimeSeq uint64, checkpoint bool, entryJSON, idempotencyKey string) (entrySeq uint64, inserted bool, err error)`.
- `type LogPosition struct { EntrySeq uint64; Line uint32 }`,
  `type SessionLogRowData struct { EntrySeq uint64; Checkpoint bool; FirstLine uint32; Body string }`,
  `type SessionLogPage struct { Rows []SessionLogRowData; NextBefore LogPosition; Head uint64 }`.
- `func (s *Store) ReadSessionLogPage(ctx context.Context, sessionID string, before LogPosition, limit, maxBytes int) (SessionLogPage, error)`.
  A `before` below the latest checkpoint is `ErrFailedPrecondition`.
- `func (s *Store) SessionLogBackfill(ctx context.Context, sessionID string, afterSeq uint64, limit int) (rows []TranscriptEntryRow, complete bool, err error)`.

Both reads run in one REPEATABLE READ transaction each.

Tests (pgtest): positions across checkpoint, valve and Postgres rows;
checkpoint chunking; no S3 read without a valve segment; a valve cut between
reads leaves no gap.

### A3 Server (lane: compass-server)

Interfaces:

- `SessionTailSink` (`runnerhub/hub.go`) adds
  `RelaySessionRow(sessionID string, row *compassv1.SessionLogRow, endedLiveSeq uint64)`.
  For a checkpoint row, `sessionTail` sends `resync_required` and closes the
  subscribers.
- `Hub.commitFrame` relays the row with the entry's `ended_live_seq` when the
  append returns `inserted`.
- `func (s *Service) ListSessionLog(ctx context.Context, req *compassv1.ListSessionLogRequest) (*compassv1.ListSessionLogResponse, error)`.
- `SubscribeAgentSession` runs the D2 steps. `toPublicFrame` maps `live`,
  `batch_pending` and `state`.
- `go/e2e/agent_ops.go` reads injections from `compass.injection` rows.

Tests: a commit between register and read arrives once; a relayed row carries
its `ended_live_seq`; each `resync_required` cause; NotFound for unknown and
forbidden targets.

### A4 Agent emitter (lane: compass-agent)

Interfaces (`packages/compass-agent/src`):

- `live-seq.ts`:
  `class LiveSeq { observe(event: AgentSessionEvent): void; current(): bigint; lastEnded(): bigint }`.
  `observe` applies the D1 rule to session events and replaces
  `EventMapper.#messageSeq`.
- `mapping.ts`: `MapOutput = OutboundFrame | UnmappedEvent | NoteOutput`,
  with
  `type NoteOutput = { kind: "note"; customType: "compass.session_error"; data: SessionErrorNote }`.
  The constructor takes the `LiveSeq`, and text deltas carry
  `live_seq = current()`.
- `session-tee.ts`: `TranscriptTeeOptions.endedLiveSeq?: () => bigint`;
  `#nextFrame` sets `TranscriptEntry.ended_live_seq` from it.
- `cli.ts` `main` makes one `LiveSeq` and passes it to the tee options and
  `CompassAgentOptions.liveSeq`. `agent.ts` `run()` calls `observe` in its
  `session.subscribe` listener, before `map`.
- `session-notes.ts`:
  `class SessionLogNotes { constructor(manager: Pick<SessionManager, "appendCustomEntry">); injection(n: InjectionNote): void; error(n: SessionErrorNote): void }`.
- `agent.ts`: `#emitInjection` calls `SessionLogNotes.injection`.
  `#emitBatchPending` sets `SessionFrame.batch_pending`.

Tests: no `SessionEvent` frame; a classifier refusal writes one
`compass.session_error`; custom entries leave a resumed model context
unchanged; two assistant messages with the same `timestamp` get different
`live_seq` values; each row's `ended_live_seq` is at least its message's
`live_seq`; a `message_update` before the last `message_end` never gets a
higher number than its message.

### A5 UI (lane: compass-frontend)

Interfaces:

- `packages/compass-client/src/session-log.ts` (devDependency on
  compass-agent's OMP version):
  `export type { SessionEntry as SessionLogEntry }`,
  `parseSessionLogLine(line: string): SessionLogEntry | undefined`, and
  `splitRow(row: SessionLogRow): { pos: SessionLogPosition; line: string }[]`.
- `apps/ui/src/session-events.ts`:
  `foldSessionLog(lines: readonly LogLine[], live: LiveState): TraceItem[]`,
  with `type LogLine = { pos: SessionLogPosition; entry: SessionLogEntry }`.
  `foldSession` and `adaptSessionEvent` go.
- `live/session-tail.ts`:
  `SessionFrameUpdate { row?: SessionLogRow; endedLiveSeq?: bigint; live?: SessionLiveEvent; batchPending?: BatchPending | null; state?: AgentSessionState; resync?: true }`.
- `store.ts` `createLiveSessions`: per session
  `{ lines, headSeq, nextBefore, live, endedLiveSeq }` and
  `loadOlder(sessionId: string): Promise<void>`; opens by session or account
  id.

Tests, written first: stored entries with no live frames render on open
(RIG-5941); open by account id after a fake redeploy; no duplicate at the
boundary; resync; the D1 delta rules in both arrival orders; two messages in
the same millisecond, with deltas before and after each row, show each text
once and never drop the newer one early.

### A6 Cleanup (lane: compass-server)

Delete `SessionEvent`, its arms and `SessionErrorKind`, and reserve the
internal `typed_event = 2`. The `TranscriptEntry` comment names the Session
Log as a consumer.

### B1 OMP upstream (owner: compass-agent)

Interfaces:

- `SessionManager` gains `get storage(): SessionStorage`.
- `ToolSession.getSessionStorage?: () => SessionStorage`, set in `sdk.ts`
  beside `getArtifactManager`.
- `ExecutorOptions.parentSessionStorage?: SessionStorage`, set beside
  `parentArtifactManager` in `task/structured-subagent.ts` and
  `vibe/runtime.ts`.
- `task/executor.ts`: the spawn and the warm `reviveSession` closure pass
  `options.parentSessionStorage` as the `storage` argument of
  `SessionManager.open` and keep their current options.
- `task/persisted-revive.ts`: the cold revive passes
  `ctx.session.sessionManager.storage` to both `peekSessionInit` and `open`.
  An indexed storage that does not hold the file, such as the tee after a
  restart, then fails the peek, so the ref stays transcript-only. Without the
  peek change, `open` would treat the file as missing and rewrite it as a new
  session.

compass-agent installs no cold-revive factory today (only the CLI `main.ts`
does), but B1 covers it so a later install keeps the tee.

Tests: with a custom parent storage, a subagent's writes go through it at
spawn, after a warm revive of a parked subagent, and after a cold revive from
`createPersistedSubagentReviverFactory`; a cold revive whose storage lacks the
file leaves the file unchanged.

### B2 Store child sessions (lane: compass-server)

```sql
ALTER TABLE agent_sessions
  ADD COLUMN lifetime_epoch BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN parent_session_id TEXT NULL REFERENCES agent_sessions(session_id),
  ADD COLUMN parent_epoch BIGINT NULL,
  ADD COLUMN subagent_id TEXT NULL,
  ADD CONSTRAINT agent_sessions_child_key CHECK (
    (parent_session_id IS NULL) = (subagent_id IS NULL)
    AND (parent_session_id IS NULL) = (parent_epoch IS NULL));
CREATE UNIQUE INDEX agent_sessions_child
  ON agent_sessions (parent_session_id, parent_epoch, subagent_id)
  WHERE parent_session_id IS NOT NULL;
```

Interfaces (`go/internal/store`):

- `BindLifetime` also increments `lifetime_epoch`.
- `func (s *Store) EnsureSubagentSession(ctx context.Context, parentSessionID, subagentID string) (sessionID string, err error)`.
- `func (s *Store) SubagentSession(ctx context.Context, parentSessionID, subagentID string) (sessionID string, ok bool, err error)`,
  the newest epoch's child.
- `LatestSessionForAccount` adds `AND parent_session_id IS NULL`.

Tests (pgtest): one child per epoch; the latest-session read skips children;
the tenant copy.

### B3 Server child routing (lane: compass-server)

Proto: `SubagentRef { string parent_session_id = 1; string subagent_id = 2; }`
as `ListSessionLogRequest` target 3; internal `SessionFrame.subagent_id = 6`
and `TranscriptEntry.subagent_id = 4`.

Interfaces: `Hub.commitFrame` and `Hub.deliverSession` resolve `subagent_id`
through `EnsureSubagentSession`, cached until the root's next bind.
`ListSessionLog` resolves `subagent` through `SubagentSession`.

Tests: a child frame reaches only the child's tail; a task id reused after a
resume opens a new child.

### B4 Agent subagent capture (lane: compass-agent)

Stacks on compass PR #2498 (RIG-5175), which also consumes
`task:subagent:lifecycle` and edits `cli.ts`.

Interfaces (`packages/compass-agent/src`):

- `session-tee.ts`:
  `TranscriptTeeBackend.mapSubagent(sessionFile: string, subagentID: string, liveSeq: LiveSeq): void`.
  Frames for an unmapped artifacts path wait until it is mapped. A mapped
  frame takes `ended_live_seq` from that subagent's `LiveSeq`.
- `subagent-log.ts`:
  `attachSubagentLog(bus: EventBus, tee: TranscriptTeeBackend, notes: SessionLogNotes, sink: FrameSink, newMapper: (liveSeq: LiveSeq) => EventMapper): () => void`,
  on the `subagentEventBus` that #2498 creates. It keeps one `LiveSeq` and
  one mapper per subagent id, and calls `observe` before `map`.
- `SessionLogNotes.subagent(n: SubagentNote): void`.

Tests: a nested child routes by its mapped path; `__advisor.jsonl` is never
routed; after a warm and a cold revive, writes route by the new `started`
event to the same child.

### B5 UI subagent rows (lane: compass-frontend)

Interfaces: `TraceItem` adds `subagent`, from `compass.subagent` entries;
`store.ts` opens a log by `{ parentSessionId, subagentId }`; a `LogPanel.tsx`
subagent row and a board subagent chip open the child log (Q8).

Tests: both clicks open the same child log.

## Tasks

- [ ] A1 Proto: history RPC, rows, live deltas.
- [ ] A2 Store: page reader, backfill, append result.
- [ ] A3 Server: row relay, `ListSessionLog`, subscribe backfill.
- [ ] A4 Agent: `LiveSeq`, live deltas, custom-entry notes.
- [ ] A5 UI: type-only import, entry fold, list then subscribe.
- [ ] A6 Delete `SessionEvent`.
- [ ] B1 OMP: subagent spawn and both revive paths use the parent's storage.
- [ ] B2 Store: child sessions and lifetime epoch.
- [ ] B3 Server: child routing.
- [ ] B4 Agent: subagent capture.
- [ ] B5 UI: subagent rows and click-through.
