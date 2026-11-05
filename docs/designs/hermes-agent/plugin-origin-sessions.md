# Plugin-origin sessions on the Discord platform

Ledger-impact: DL-648, DL-649

A design record, not shipped behavior. Every current-behavior claim names the file and function it
was read from, on the fork's `rigel-release` at `c4093027`; everything else is proposed.

This record implements Matt's RIG-8011 rulings for RIG-7938: an operator's assistant profile runs
intake on `cotal`, and its handoff plugin posts notes into Discord forum threads the bot created.

## Decisions

1. **Q1 A: `discord` on `cotal`** (DL-648). Cotal turns may get the core `discord` toolset, under a
   mesh scope.
2. **Q2 B: plugin-origin sessions** (DL-649). An allow-listed plugin may start a session at a
   Discord thread origin under the session authority. Approval prompts for secret actions and
   similar actions still reach the operator.
3. **Plugin notes run narrow.** Every row that a plugin with `allow_origin_sessions` injects, in a
   bootstrapped or an existing session, runs in a fresh context with the `cotal` toolset list and
   the mesh scope; `discord_admin` is absent. The note text comes from a peer whoever started the
   thread, and this is the conservative choice; Matt can widen it at merge.

## Problem / Intent

Three refusals block the use case. `_TOOLSET_PLATFORM_RESTRICTIONS` (`hermes_cli/toolset_scope.py`)
keeps `discord` off `cotal`. `_discord_message_admission` (`plugins/platforms/discord/adapter.py`)
drops the bot's own messages. `_plugin_origin_admissible` and `_dispatch_plugin_origin_injection`
(`gateway/run_plugin_injection.py`) refuse an origin with no `committed_route_envelope` or
session, which a bot-created thread lacks.

Intent: scoped Discord on Cotal, and a note that starts a turn at once whose approvals reach the
operator.

## Approach

### Q1: toolset and mesh scope

Add `"cotal"` to the `discord` row. `discord` stays in `_DEFAULT_OFF_TOOLSETS`
(`hermes_cli/tools_config.py`); `discord_admin` stays Discord-only.

Peer text drives cotal turns. In `tools/discord_tool.py`, `_require_post_scope` allows any channel
when `discord.post_channels` is unset, `_create_thread` has no scope check, and `_fetch_messages`
reads any channel. A new `_require_mesh_scope` covers every core action on a peer-mesh or
plugin-origin turn, with no hard-coded channel set (ruled out by RIG-7938):

- writes need a set `discord.post_channels` that holds the target or its thread parent;
- reads refuse DM and group-DM channels (types 1 and 3) and channels outside the guilds of the
  `post_channels` entries. With no list, reads are refused.

### Q2: plugin-origin bootstrap

A new descriptor, `plugin_origin_v1`, admits a plugin turn to a route with no human envelope. It
never resembles `native_text_v1`, and `snapshot_automation` is unchanged.

Admission requires all of:

- the plugin's own config sets `allow_gateway_injection: true` and the new
  `allow_origin_sessions: true`, the allow-list that keeps other injectors out;
- `_plugin_origin_source` passes (own profile, live adapter, routing);
- a Discord thread origin (`chat_type == "thread"`, `thread_id == chat_id`) whose `owner_id`,
  read by a REST GET, is the bot user;
- the profile does not set `thread_sessions_per_user`;
- the emergency stop is not engaged (`check_paused` in `agent/estop.py`); internal events skip
  `_hm_estop_gate`.

No user ID is checked: `_plugin_injection_authorized` needs an allowlisted `user_id`
(`_principal_authorized` in `gateway/authz_mixin.py`), so the plugin would have to claim the
operator. The source user is `plugin:<id>` with `is_bot: true`. Threads are shared unless
`thread_sessions_per_user` (`build_session_key`), so the operator's reply joins the session.
Admission calls `_threads.mark_async`, so `_in_bot_thread` admits that reply without a mention.

Every injection from a plugin with `allow_origin_sessions` is scheduled in a fresh
`contextvars.Context()`: `_schedule` (`gateway/session_authority.py`) creates the drain in the
caller's context, and `_get_approval_mode` honours a bound hosted-room policy. Admission reserves
and publishes the route as `admit_native` does, then commits `{'text', 'plugin_origin_v1'}` through
`admit_session_input` under principal `automation:<route>`, with `identity`, `plugin`, `home`,
`owner`, `route` and `target`. Later notes take this path until the operator's first reply.

`check` runs at the drain pre-claim and at startup recovery, repeating every admission test plus
shape, principal, owner, route and target. A refused row settles `cancelled` as `cancel_queued`
does (`_settle_cancelled`) and never pauses the thread: it is not the operator's input. The
non-LOCAL pre-claim becomes exhaustive: a head with any unknown descriptor key raises
`permission_denied`.

`restore` builds an internal event with `allow_gateway_control=False`, so `_handle_message` skips
`_hm_pending_reply_intercepts`: a note cannot answer an approval. `execute_admission` binds a
`plugin_origin_turn` marker for a `plugin_origin_v1` row and for an automation row whose identity
(`["plugin", <id>, ...]`, from `_admit_plugin_injection`) names a plugin with the key; an
unreadable config counts as set. Under the marker, `_resolve_enabled_toolsets_for_source`
(`gateway/run_turn.py`) resolves `platform_toolsets.cotal` and `_require_mesh_scope` applies.

### How approvals reach the operator

1. `_set_session_env` (`gateway/run.py`) binds `platform=discord`, so
   `_is_gateway_approval_context` holds and `_presence` (`tools/approval.py`) keeps `is_gateway`.
2. The route is in `authority.sessions`, so `TurnRunner.__init__` (`gateway/run_turn_runner.py`)
   sets `_approval_owner`, and `_approval_notify_sync` sends the card into the thread.
3. A secret guard's `approve` with `review: true` runs `request_tool_approval` with `review_first`:
   a reviewer DENY blocks, and any other verdict asks the human.
4. `_component_check_auth` (`plugins/platforms/discord/adapter_component_auth.py`) accepts an
   allowlisted user confirmed by `_component_live_auth`, allow-all, a role-allowlist member or a
   pairing-store approval. With `discord.approval_mentions`, the card mentions allowlisted users.
5. A plugin turn is never a viewer turn (`is_viewer_turn_with_approver`), so the whole-script
   `execute_code` gate stays.
6. A plugin-origin card offers once or deny only, and nothing persists, as for a smart-DENY
   override in `_human_decision`.

Preconditions, the same as for the operator's own turns in that thread: `_run_approval_gate`
approves under yolo or `approvals.mode: off`; `is_approved` honours earlier permanent grants, which
are the operator's own; smart mode keeps the operator's policy, so the guardian may approve a
dangerous command without a card. A `review: true` rule asks the human in every other mode.

## Alternatives considered

- **Synthesize a native envelope.** It mints provenance the platform never delivered.
- **Drop the committed-envelope check.** Any injecting plugin could open any chat.
- **Run the first turn outside the FIFO.** No ordering, no recovery, no `_approval_owner`.
- **Reuse the cron gate on Cotal.** `_require_cron_scope` limits reads to the `post_channels`
  entries, which blocks the home channel.
- **Leave mesh reads unscoped.** A peer could read the operator's DM with the bot. Ruled: refuse
  DM channels and channels outside the operator's guilds.
- **Note-only bootstrap, or a webhook author that mentions the bot under `allow_bots`.** Rejected:
  the operator already ruled that a note wakes its post (RIG-7983 Q2).
- **Pause the FIFO on a refused plugin row.** A revoked plugin would block the operator's replies
  until `/reset` (`pause_notice`).

## Global Constraints

- One PR on `rigel-release`, at or after `c4093027`. `create_task(context=...)` needs Python 3.11,
  the `requires-python` floor.
- `committed_route_envelope`, `snapshot_automation`, `_event_clause` and `check_native_route` keep
  their behavior; no plugin check passes `allow_adapter_delegation=True`.
- In `tools/approval.py`, only the plugin-origin card scope changes.
- Tests use fixtures, never live Discord. Fork docs name no deployment IDs.

## Plan

### Task 1: `discord` on `cotal`, with the mesh scope

Interfaces:

```python
# hermes_cli/toolset_scope.py
_TOOLSET_PLATFORM_RESTRICTIONS = {"discord": {"discord", "cron", "cotal"}, "discord_admin": {"discord"}}
# gateway/session_ingress.py
plugin_origin_turn: ContextVar[bool] = ContextVar('plugin_origin_turn', default=False)
# tools/approval_context.py
def is_plugin_origin_turn() -> bool: ...
# tools/discord_tool.py
_MESH_WRITE_ACTIONS = frozenset({"create_thread", "create_forum_post", "post_message"})
_DM_CHANNEL_TYPES = frozenset({1, 3})
def _require_mesh_scope(token: str, action: str, *, channel_id: str, guild_id: str) -> None: ...
```

`_require_mesh_scope` returns unless `_is_peer_mesh_approval_context()` or `is_plugin_origin_turn()`
holds; `_run_discord_action` calls it after `_require_cron_scope`.

Tests: `test_discord_toolset_allowed_on_cotal_when_configured` and
`test_discord_toolset_not_implicit_on_cotal` in `tests/hermes_cli/test_tools_config.py`.
`TestMeshScope` in `tests/tools/test_discord_tool.py`, on a cotal turn and under the marker: writes
outside the list or with no list are refused; a thread under a listed forum passes; DM and
other-guild reads are refused; Discord turns are unchanged. Docs: `toolsets-reference.md`,
`tools-reference.md`.

### Task 2: bootstrap admission

Interfaces:

```python
# gateway/plugin_origin_session.py (new; beside gateway/plugin_keyless_inject.py)
class PluginOriginRefused(Exception): ...  # .reason: str
def allowed(plugin_home: Path, plugin_id: str) -> bool: ...
def eligible(runner, source: SessionSource) -> bool: ...
async def admit(runner, authority, adapter, source: SessionSource, *, content: str,
                plugin_id: str, plugin_home: Path) -> bool: ...
async def check(authority, ref: SessionRef, row: dict, *, source: SessionSource, adapter) -> str: ...
def restore(authority, ref: SessionRef, row: dict) -> MessageEvent: ...
def narrowed(authority, row: dict) -> bool: ...
# plugins/platforms/discord/adapter.py, DiscordAdapter
async def thread_owned_by_self(self, thread_id: str) -> bool: ...
```

`allowed` reads the plugin's config under `_plugin_home_scope` and fails closed.

Wiring:

- `run_plugin_injection.py`: `_plugin_origin_admissible` accepts an origin with no envelope when
  `eligible` and `allowed` hold. `_dispatch_plugin_origin_injection` takes `plugin_home` and calls
  `admit`. For an allowed plugin, `_schedule_plugin_message_injection` uses
  `loop.create_task(coro, context=contextvars.Context())`, or off the loop
  `loop.call_soon_threadsafe(start, context=contextvars.Context())`.
- `session_authority.py`: `_claim_next` dispatches on the head's descriptor key; a refused
  `plugin_origin_v1` head returns `('refused', first)`, and `_drain` settles it outside the claim
  lock. In `recover_native_sessions`, a session with no native row whose queued rows are all
  `plugin_origin_v1` binds `LiveSession(available_source, route)` once `check` matches
  `available_source`'s session key to the descriptor's `route` and `target`.
- `session_ingress.py`: `execute_admission` calls `restore` for `plugin_origin_v1` and binds
  `plugin_origin_turn` when `narrowed` holds; `row_turn_author` returns `plugin:<id>`.
- `run_turn.py`: under the marker, `_resolve_enabled_toolsets_for_source` reads the `cotal` list
  through `_get_platform_tools`.

Tests in a new `tests/gateway/test_plugin_origin_session.py`, on a real `GatewayRunner` and
`SessionAuthority` with a stub Discord adapter:

- Success: one session, one `plugin_origin_v1` row, a `plugin:` source user, a marked thread.
- Refusals leave no session: a missing key, a non-thread origin, a thread the bot does not own,
  `thread_sessions_per_user`, another profile, an engaged estop.
- A second note by `session_key` is admitted. An unmentioned operator reply is admitted natively.
- Revocation or an engaged estop before the claim settles the row `cancelled`; the turn never runs,
  and the operator's next reply still runs.
- An unknown descriptor head pauses with `permission_denied`.
- Restart recovery runs a queued row, and settles it after a revocation.
- Two origin injections racing one native message give three ordered rows in one session.
- The turn lacks `discord_admin`, and `post_message` outside the list is refused.
- In an existing session with an operator turn, an allowed plugin's note runs under the marker
  without `discord_admin`; another plugin's note runs unchanged; an unreadable config binds it.

`test_origin_injection_is_refused_up_front_until_the_chat_holds_a_human_turn` stays green: its
plugin lacks `allow_origin_sessions`.

### Task 3: approvals reach the operator

Interfaces:

```python
# tools/approval.py, _human_decision
one_shot = smart_denied or approval_context.is_plugin_origin_turn()
# card data, transport flags and grant() use one_shot where they use smart_denied today
```

A new `tests/gateway/test_plugin_origin_approval.py` runs a new fixture,
`tests/gateway/fixtures/plugin_origin_approval_peer.py`, in a subprocess, like
`test_shared_session_controls.py`: a real `GatewayRunner`, `SessionAuthority` and `AIAgent` on the
loopback `ModelPeer`, a Discord adapter recording `_send_exec_approval_prompt`, and a plugin with
both keys whose `pre_tool_call` returns `approve` with `review: true`. Each case bootstraps a fresh
thread, in manual mode unless stated:

- Reviewer approve or escalate: the card reaches the thread, mentions the operator and offers once
  and deny only; "once" runs the effect, and a stale "always" answer persists nothing.
- Reviewer deny: no card, no effect. A human deny or a timeout: no effect.
- A dangerous `terminal` command with no plugin rule gets a card.
- An injection from a cotal turn's executor thread, with a bound room policy whose mode is off,
  still gets a card, at a bootstrap and in an existing session with an operator turn.
- Parity with the operator's own turn in the thread: under yolo, mode off, a permanent grant, or a
  smart guardian approve, both run without a card. A `review: true` rule in smart mode gets a card.

Docs: the origin section of `website/docs/user-guide/features/plugins.md`.

## Tasks

- [ ] Task 1: `discord` on `cotal`, the mesh scope and the plugin-origin marker.
- [ ] Task 2: `plugin_origin_v1` admission, revalidation, settlement, recovery and tests.
- [ ] Task 3: one-shot plugin-origin cards, end-to-end approval tests and plugin docs.

## Spec impact

No spec here changes; fork docs change as each task lists. After the repin, the operator's profile
adds `discord` to `platform_toolsets.cotal`, sets both keys on its handoff plugin, and keeps
`discord.post_channels` and `discord.approval_mentions: true`.

## Ledger impact

- [DL-648](decisions/session/DL-648.md): `discord` on `cotal`, mesh-scoped.
- [DL-649](decisions/session/DL-649.md): plugin-origin sessions.

## Sources

- RIG-8011 (rulings), RIG-7938 and RIG-7983 (parent decisions).
- `RigelBuild/hermes-agent`, `rigel-release` at `c4093027`: the files and functions named above.
