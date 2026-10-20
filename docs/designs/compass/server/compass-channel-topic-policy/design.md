# Compass channel topic policy

Tracking: RIG-5951 (owner lane compass-server).
Ruling: Matt, 2026-10-19, Option A. `routing` is automation-only; the
supervisor reads it, triages, and dispatches. DMs default to AGENTS_ONLY;
humans reply inside existing threads. DL-631 and DL-632 record this for
agent–agent DMs and for topic starts in `routing`. DMs with a user party
are Open Question 2. Matt also said `routing` is automation-only for
*posting*; Open Question 1 covers the conflict that creates.
Related: RIG-5950 (compass#2492) makes the UI send `create_topic` when it
starts a topic. RIG-5958 (compass#2499) makes `InsertTopicIgnore` report
whether it created a row.

## Open Questions

Questions 1 and 2 are load-bearing. The four are independent. T1–T4 are
written against the recommendations; T5 runs only if Question 1 is A.

1. **What does "automation-only for posting" mean in `routing`?** It cannot
   be literal. The supervisor is an agent, and it must post in the session
   topic: `Dispatcher.OnCommsEvent` (`go/internal/linearagent/dispatcher.go`)
   "emits one `response` on the Manager's first post in an armed session
   topic", and on the routing fallback that Manager is the supervisor. With
   no supervisor post, every cold delegation stays "Thinking" in Linear.
   Matt is a routing member (`EnsureLinearRoutingChannel` adds the admin),
   so topic policy alone still lets him post replies.
   - A. A new post-policy value `AGENTS_AND_AUTOMATION`: agents and system
     accounts post, users do not. Server-enforced. The supervisor and the
     bridge still post. The post gate reuses the author-kind read T2 already
     adds. Cost: one enum value, a wider CHECK, one gate branch, and caller
     kind in the UI `canPost` (T5).
   - B. Topic policy only. `routing` stays OPEN; the UI hides the composer
     from users there. Human replies stay possible on the server. This is a
     UI-only gate, which Matt rejected for topic starts (Option C).
   - C. Literal automation-only posting. The supervisor's triage reply goes
     somewhere other than the routing topic (Linear directly, or the owning
     Manager's channel), and `OnCommsEvent` changes to match. This changes
     the DL-255 flow and is the largest change.
   - **Recommendation:** A. It is the closest server-enforced reading: only
     automation starts work, only agents and automation post, and the
     supervisor reply that Linear needs still lands.
2. **Does AGENTS_ONLY cover a DM where a user is a party?** `OpenDM`
   (`go/internal/comms/comms.go`) accepts a user caller, and
   `TestOpenOwnerAgentDMSurvivesOwnerHandleRename` pins "owner-agent DMs when
   the user is an actual party". Under a flat AGENTS_ONLY default, a user who
   opens a DM to task an agent cannot post the first message. The backfill
   would lock every existing user↔agent DM the same way.
   - A. Set the birth value from the parties: a DM between two agents is
     AGENTS_ONLY; a DM with a user party is ANY_MEMBER. It is still a stored
     field set once at birth, not a kind rule at the gate.
   - B. AGENTS_ONLY on every DM. A user-opened DM stays empty until the agent
     posts; the UI hint must say so.
   - **Recommendation:** A. "Humans reply inside existing threads" fits agent
     queues. A DM the user opened is the user's own conversation.
3. **May a member change the topic policy of a DM or of `routing`?**
   - A. No. `SetChannelPolicy` refuses with INVALID_ARGUMENT, as it does for
     "OWNER_ONLY is not supported on a TREE channel"; setting the current
     value is a no-op. This is a kind rule at the mutation gate, the kind of
     rule Option B rejected.
   - B. Yes, through the normal `SetChannelPolicy` gate. Agents have no
     policy tool, so only a human member can do it. The routing seed puts
     `routing` back when it runs; a DM keeps the change.
   - **Recommendation:** B. No kind rule, no extra code.
4. **What topic policy does a converted DM get?** A member add converts a DM
   to a named CHANNEL (DL-296). `ConvertDMChannel` already resets
   `mandatory_subscription`.
   - A. Reset to ANY_MEMBER. Consequence: any member, including the pulled-in
     human owner, can convert a DM by adding a third party, and get a channel
     where humans start topics. A fresh `open_dm` then mints a new DM with
     the birth value.
   - B. Keep the DM's value.
   - **Recommendation:** A. A converted DM is a normal channel.

## Problem / Intent

Any channel member can start a topic today. `PostMessage` creates the topic
when `create_topic` is true, and the UI shows "New topic" on every channel.
Matt wants two channels to work like queues: in a DM only agents start
threads, and in `routing` only automation starts threads. Humans reply in
existing topics. `post_policy` (OPEN or OWNER_ONLY) controls who may post. It
does not control who may start a topic.

## Approach

### D1 — Policy field and values

Add a channel-level field `topic_policy` next to `post_policy`. This is the
shape DL-136 chose for post authority: a policy field, never a `ChannelKind`
rule or a name rule. Matt rejected those (Option B), and a UI-only gate
(Option C).

| Value | Who may start a topic |
| --- | --- |
| ANY_MEMBER | Every member (today's behavior) |
| AGENTS_ONLY | Agent accounts and system accounts |
| AUTOMATION_ONLY | System accounts only |

"Automation" means a system account. The server sender (`@compass`) and the
Linear bridge (`@linear`) are both system accounts: `EnsureSystemAccount` and
`EnsureLinearBridgeAccount` (`go/internal/store/accounts.go`) both call
`ensureSystemSubtypeAccount`. A system account cannot get a token:
`IssueToken` (`go/server/service.go`) refuses any `acc.System != nil`. So no
human or agent can act as automation. The limit: AUTOMATION_ONLY admits only
in-process server connectors. A future token-authenticated source (the
`routing` constant's comment names "later alert sources") cannot start a
routing topic unless it is built as a system account.

Scope: the policy controls **topic creation only**.

- A post that names an existing topic is a reply and is never gated, even
  with `create_topic: true`. After RIG-5950 the UI sends `create_topic: true`
  from "New topic", and an existing name resolves to that topic.
- Reviving an archived topic is a reply.
- `UpdateTopic` rename and merge are out of scope; their only gate is
  participation. A human member can rename a bridge topic in `routing`. The
  dispatcher's miss path then mints a fresh topic as the bridge, which the
  policy allows.

### D2 — Defaults and backfill

- A DM is born with its party-derived value (Open Question 2): AGENTS_ONLY
  between two agents, ANY_MEMBER with a user party. `UpsertDMChannelTx`
  (`go/internal/store/dm.go`) computes it from `DMChannelSpec.Members`.
- The routing channel is born AUTOMATION_ONLY. `InsertLinearRoutingChannel`
  (`queries/linear_routing.sql`) writes it. `ReassertLinearRoutingShape`
  sets it too and checks it in its drift `WHERE`.
- Every other channel defaults to ANY_MEMBER. This includes home channels:
  `InsertHomeChannel` mints `kind = CHANNEL`, so the Setup thread (DL-192)
  and bridge topics in a Manager home channel do not change.
- An unset `topic_policy` on `CreateChannelRequest` means the server default.
- Existing rows: migration `0005` backfills agent↔agent DMs to AGENTS_ONLY
  and the routing channel to AUTOMATION_ONLY. The migration is the guarantee.
  The reassert is not a boot guarantee: `seedRootSupervisor`
  (`go/server/serve_seed.go`) runs on the runner-ready hook, only when the
  Linear bridge is configured, and returns before
  `EnsureLinearRoutingChannel` on the non-root-supervisor and non-empty-tree
  branches.

### D3 — Enforcement at the one topic-mint point

All topic creation goes through `resolveTopicForAppend`
(`go/internal/store/messages.go`), the only caller of `InsertTopicIgnore`.
It serves `AppendMessage` (the `PostMessage` path: human RPC,
`PostAsAccount`, `PostAsAccountByName`) and `GetOrCreateTopic`
(`go/internal/store/topics.go`, the Linear dispatcher). One check covers
every caller.

The check runs only when the insert created a row (the `created` flag from
RIG-5958). It reads the channel's `topic_policy` and the author's account
kind. A refusal returns `ErrPermissionDenied`, and the transaction rolls
back, so the topic never commits. Concurrent creates converge by the unique
index as today; only the author whose insert made the row is checked.

`AppendMessage` keeps its order: membership (`requireChannelMember`; a
non-member gets NOT_FOUND), then post policy, then topic resolution. A
member refused by topic policy gets PERMISSION_DENIED through `edgeError`
(`go/internal/comms/context.go`). This extends DL-383 to topic policy: a
member can already read the channel, so the distinct code leaks nothing.

Error text tells an agent what to do next:

- `channel "<name>" is agents-only: only an agent or automation can start a
  topic; reply in an existing topic`
- `channel "<name>" is automation-only: only automation can start a topic;
  reply in an existing topic`

### D4 — Who may change it

`SetChannelPolicy` stays the only post-creation mutation (DL-136). It gains
an optional `topic_policy`; unset leaves the value unchanged, so a caller that
sets only `post_policy` cannot reset it. The existing gate applies: the actor
must be a member, and once a channel has an owner only that owner may change
policy. There is no extra refusal for DMs or `routing` (Open Question 3).
`CreateChannel` takes an initial value.

### D5 — Agent exposure

No new agent tool and no new `CommsCallRequest` arm. Agents cannot set the
policy; the relay has no policy arm. An agent learns the policy from the
refusal text. Two prompt lines teach the rule:

- `config/prompts/supervisor/SYSTEM.md`, "How you communicate", says "an
  unknown name creates the topic". Add: in `routing` the Linear bridge starts
  every topic; read it, triage, and reply inside the existing topic.
- `config/skills/comms-playbook/SKILL.md` says "a new topic needs
  `create_topic: true`". Add: a channel's topic policy can refuse that; on
  PERMISSION_DENIED, reply in an existing topic.

Agents may start topics in agent DMs, so the spawn auto-open DM and
`comms_dm` keep working (DL-296).

### D6 — UI gate: a channel field, not a per-caller capability

The UI decides from `Channel.topic_policy` and the caller's account kind
(`store.caller()`, kind "user", "agent" or "system"). A per-viewer bit is
possible, since `subscribe.go` already reshapes `ChannelChanged` per actor
(`trimRemovedToActor`). It is not worth it: it needs an account lookup per
subscriber per event, and the server stays the authority either way. The cost
of this choice: `canStartTopic` copies the server rule in TS, so T4 tests the
same 3 × 3 table as T2.

`NewTopic` (`apps/ui/src/components/ChannelView.tsx`) is hidden when the
caller may not start a topic. One hint line renders in its place, for example
"Agents-only channel — reply in an existing topic." The owner-only path keeps
its disabled-with-hint form.

### Prior records

- [Manager comms substrate §A2](../../agent/compass-manager-comms-substrate/design.md#a2--rig-1722-coordination-channel--a-channel-level-post_policy--mandatory_subscription-auto-provisioned-from-tree-edges):
  DL-136 (channel-level policy fields, `SetChannelPolicy`) and DL-383
  (PermissionDenied for a member refused by OWNER_ONLY).
- [Agent peer-DM](../../agent/compass-agent-peer-dm/design.md#ledger-delta):
  DL-296 (DM birth shape, convert on add) and DL-293 (`create_topic` gates
  creation everywhere; this record adds who may create).
- [Zulip threading model](../compass-zulip-threading-model/design.md): DL-098
  (every message is in a named topic).
- [Channels in the agent tree](../../ui/compass-channels-in-agent-tree/design.md):
  TREE channels take topic policy unchanged.
- [Linear agent responder](../compass-linear-agent-responder/design.md#part-2--routing-a-delegated-linear-session-to-a-stable-manager):
  DL-255 (the routing channel).

## Global Constraints

- Wire changes are additive. Follow the comms.proto convention (buf.yaml
  exempts it from `ENUM_ZERO_VALUE_SUFFIX`): a meaningful zero value, no
  `_UNSPECIFIED`. Fields: `Channel.topic_policy = 13`,
  `CreateChannelRequest.topic_policy = 8` (`optional`),
  `SetChannelPolicyRequest.topic_policy = 5` (`optional`).
- Wire and store numbers match: ANY_MEMBER = 0, AGENTS_ONLY = 1,
  AUTOMATION_ONLY = 2. Column default 0 keeps old rows and old inserts right.
- Never edit migrations 0001–0004; the runner refuses an edited applied
  migration. The schema change is `0005_channel_topic_policy.sql`.
- `channels` has FORCE row-level security. A backfill `UPDATE` runs as
  `compass_system` (BYPASSRLS), or it can match zero rows silently.
- A member refused by topic policy gets PERMISSION_DENIED; a non-member
  keeps NOT_FOUND. The text names the channel and says to reply in an
  existing topic. A reply to an existing topic is never refused.
- Regenerate sqlc and proto output, never hand-edit (`compass-go:sqlc-gen`,
  `compass-go:sqlc-drift`, `compass-proto:gen`, `compass-proto:drift`).

## Plan

Stack: T1 → T2 → T3 → T4 (→ T5), one PR each. T2 needs RIG-5958's `created`
flag; if RIG-5958 has not landed, T2 stacks on it.

### T1 — Proto: enum and fields (compass-server)

Interfaces (produces), in `proto/compass/v1/comms.proto`:

```proto
// Who may start a topic. Replies to an existing topic are never gated.
enum ChannelTopicPolicy {
  CHANNEL_TOPIC_POLICY_ANY_MEMBER = 0;
  // Agent and system accounts; users reply only.
  CHANNEL_TOPIC_POLICY_AGENTS_ONLY = 1;
  // System accounts only (the server sender and in-process connectors).
  CHANNEL_TOPIC_POLICY_AUTOMATION_ONLY = 2;
}

message Channel { ChannelTopicPolicy topic_policy = 13; }
// Unset = the server default for the kind and parties.
message CreateChannelRequest { optional ChannelTopicPolicy topic_policy = 8; }
// Unset = unchanged.
message SetChannelPolicyRequest { optional ChannelTopicPolicy topic_policy = 5; }
```

Tests: `compass-proto:gen`, `compass-proto:drift` and buf lint stay green.
Generated Go and TS compile (`compass-agent:typecheck`,
`compass-ui:typecheck`).

### T2 — Store: column, backfill, enforcement (compass-server)

Interfaces (produces), in `go/internal/store`:

```go
type ChannelTopicPolicy int32 // mirrors ChannelPostPolicy (types.go)

const (
	ChannelTopicPolicyAnyMember      ChannelTopicPolicy = 0
	ChannelTopicPolicyAgentsOnly     ChannelTopicPolicy = 1
	ChannelTopicPolicyAutomationOnly ChannelTopicPolicy = 2
)

// Channel gains:        TopicPolicy ChannelTopicPolicy
// ChannelPolicy gains:  TopicPolicy *ChannelTopicPolicy // nil = default (create) / unchanged (set)

// birthTopicPolicy: CHANNEL -> ANY_MEMBER; DM -> AGENTS_ONLY unless a party
// is a user account, then ANY_MEMBER (Open Question 2 = A; under B, always
// AGENTS_ONLY).
func birthTopicPolicy(ctx context.Context, q *db.Queries, kind ChannelKind, parties []AccountID) (ChannelTopicPolicy, error)

// requireTopicMint returns ErrPermissionDenied when author may not start a
// topic in channelID. resolveTopicForAppend calls it only when its insert
// created the row.
func requireTopicMint(ctx context.Context, q *db.Queries, channelID string, author AccountID) error
```

Migration `migrations/0005_channel_topic_policy.sql`:

```sql
ALTER TABLE channels
    ADD COLUMN topic_policy SMALLINT NOT NULL DEFAULT 0
    CHECK (topic_policy IN (0, 1, 2));

SET LOCAL ROLE compass_system;
-- Agent<->agent DMs only (Open Question 2 = A; under B, drop the count).
UPDATE channels c SET topic_policy = 1
 WHERE c.kind = 1
   AND (SELECT count(*) FROM channel_members cm
          JOIN agent_accounts aa ON aa.account_id = cm.account_id
         WHERE cm.channel_id = c.id) >= 2;
-- Same key as GetLinearRoutingGroup (owner visibility, top level).
UPDATE channels c SET topic_policy = 2
  FROM channel_groups g
 WHERE c.group_id = g.id AND g.name = '__linear__' AND g.visibility = 0
   AND g.parent_group_id IS NULL AND c.name = 'routing';
RESET ROLE;
```

`RESET ROLE` matters: `applyMigration` (`store.go`) writes
`schema_migrations` in the same transaction after the SQL. `CreateChannelGroup`
refuses `__linear__`, so only `EnsureLinearRoutingChannel` makes that group.

Queries:

- New `GetTopicMintPolicy :one`: `topic_policy`, `name`, and `EXISTS` flags
  for the author in `agent_accounts` and in `system_accounts`.
- New `AnyUserAccount :one`: `EXISTS` over `user_accounts` for an id array.
- `InsertChannel` and `InsertDMChannel` take `topic_policy`;
  `InsertLinearRoutingChannel` writes 2.
- `ReassertLinearRoutingShape` sets `topic_policy = 2` and adds
  `OR topic_policy <> 2` to its `WHERE`.
- `UpdateChannelPolicy` sets
  `topic_policy = COALESCE(sqlc.narg(topic_policy), topic_policy)`.
- `ConvertDMChannel` sets `topic_policy = 0` (Open Question 4).
- Every `Channel` projection (`GetChannel`, `ListChannels`,
  `ChannelsByNameForViewer`) returns `topic_policy`; `channelFromRow` maps it.

Go: `CreateChannel` writes `*p.TopicPolicy`, or `birthTopicPolicy` with the
actor plus `MemberAccountIDs`. `UpsertDMChannelTx` writes `birthTopicPolicy`
with `spec.Members`. `SetChannelPolicy` writes the value when non-nil.

Tests (pgtest):

- Matrix: 3 policies × author {user, agent, system} on a new topic name.
  Only the D1 table rows pass. A refused create leaves no `topics` row.
- Healthy subject: on each policy, every member kind replies to an existing
  topic with `Create: true` and revives an archived topic. All pass.
- A non-member gets `ErrNotFound`, not `ErrPermissionDenied`.
- `GetOrCreateTopic` as the bridge into `routing` passes; the supervisor
  starting a topic there is refused.
- Birth: agent↔agent DM reads 1; user↔agent DM reads 0; plain channel 0.
- Migration: apply through 0004, insert both DM shapes, the routing channel
  and a plain channel, apply 0005, assert 1, 0, 2, 0. Run as a non-superuser
  role so RLS is real. `TestCheckContiguous` and `TestEmbeddedMigrationsLoad`
  stay green.
- Drift: set routing to 0, run `EnsureLinearRoutingChannel`, read back 2.
- `SetChannelPolicy` with nil keeps the value and with a value sets it. A
  converted DM reads 0.

### T3 — Comms edge and prompts (compass-server)

Interfaces (consumes T1, T2; produces), in `go/internal/comms/mapping.go`:

```go
func channelTopicPolicyToWire(p store.ChannelTopicPolicy) compassv1.ChannelTopicPolicy
// nil in -> nil out; an unknown value -> InvalidArgument.
func channelTopicPolicyFromWire(p *compassv1.ChannelTopicPolicy) (*store.ChannelTopicPolicy, error)
```

- `channelToWire` sets `TopicPolicy`.
- `CreateChannel` and `SetChannelPolicy` pass the mapped optional through.
- `PostMessage` needs no change; `edgeError` already maps
  `ErrPermissionDenied` to PERMISSION_DENIED.
- The D5 prompt lines.

Tests (pgtest): a user starting a topic in an agent↔agent DM gets
PERMISSION_DENIED with the agents-only text, and the same user replying to an
agent's topic passes. A user opens a DM with an agent and starts a topic
(Open Question 2 = A). `SetChannelPolicy` round trip, and `ChannelChanged`
carries the new value. Fixtures that start an agent-DM topic as a user move
to an agent author.

### T4 — UI gate (compass-ui)

Interfaces (consumes T1 TS output and RIG-5950), in `apps/ui/src`:

```ts
// comms-stub.ts
export type ChannelTopicPolicy = "any_member" | "agents_only" | "automation_only";
// Channel gains: topicPolicy: ChannelTopicPolicy

// comms.ts
export function canStartTopic(channel: Channel, caller: Account): boolean;
// any_member -> true; agents_only -> caller.kind !== "user";
// automation_only -> caller.kind === "system"
```

- `live/adapt.ts`: a total `TOPIC_POLICY` map next to `POST_POLICY`.
- `ChannelView.tsx` `NewTopic`: when `canStartTopic` is false, render only
  the hint line (D6). The owner-only path does not change.
- Fixtures: every `Channel` literal gets `topicPolicy`. The stub models a
  home channel as `kind: "dm"`; give it "any_member" to match the server.

Tests: `canStartTopic` table (3 × 3, same rows as T2).
`ChannelView.policy.test.tsx`: a user on an agents-only DM sees no
`.new-topic-name` and sees the hint; a user on an any-member channel sees the
affordance; the owner-only cases stay green. Adapter map test.

### T5 — Routing post policy (only if Open Question 1 = A)

Interfaces:

```proto
enum ChannelPostPolicy { CHANNEL_POST_POLICY_AGENTS_AND_AUTOMATION = 2; }
```

- Migration `0006`: replace `channels_post_policy_check` with
  `CHECK (post_policy IN (0, 1, 2))`.
- `AppendMessage`: on value 2, a user author gets `ErrPermissionDenied`
  ("channel %q takes posts from agents and automation only").
  `SetChannelPolicy`: value 2 needs an empty owner, like OPEN.
- `EnsureLinearRoutingChannel` passes 2 to `InsertLinearRoutingChannel` and
  `ReassertLinearRoutingShape`. The bridge and the supervisor still post.
- UI: `canPost(channel, caller: Account)`; value 2 is false for a user.

Tests: the user, agent and system matrix on a post into an existing topic;
the supervisor's reply in a routing topic fires `OnCommsEvent`
(`TestDispatcherManagerReplyEmitsResponse` stays green).

## Tasks

- [ ] T1. Proto `ChannelTopicPolicy` enum and three fields; regen. (DL-631)
- [ ] T2. Store column, migration 0005 backfill, birth values, routing
  reassert, `requireTopicMint` in `resolveTopicForAppend`,
  `SetChannelPolicy` write. (DL-631, DL-632)
- [ ] T3. Comms mapping, `CreateChannel` and `SetChannelPolicy` edge,
  supervisor prompt and comms-playbook lines. (DL-631)
- [ ] T4. UI `topicPolicy`, adapter map, `canStartTopic`, `NewTopic` gate.
  (DL-632)
- [ ] T5. Routing post policy `AGENTS_AND_AUTOMATION`, only if Open
  Question 1 = A.
