# Plugin-origin sessions on the Discord platform

Ledger-impact: DL-648, DL-649

A design record, not shipped behavior. Every current-behavior claim names the file and function it
was read from, on the fork's `rigel-release` at `c4093027`; everything else is proposed.

This record implements Matt's RIG-8011 rulings for RIG-7938: an operator's assistant profile runs
intake on `cotal`, and its handoff plugin posts notes into bot-created Discord threads.

## Decisions

1. **Q1 A: `discord` on `cotal`** (DL-648). Cotal turns may get the core `discord` toolset, under a
   mesh scope.
2. **Q2 B: plugin-origin sessions** (DL-649). An allow-listed plugin may start a session at a
   Discord thread origin under the session authority. Approval prompts for secret actions and
   similar actions still reach the operator.
3. **Plugin notes run narrow.** Every row that a plugin with `allow_origin_sessions` injects, in any
   session, is a plugin-origin turn: a fresh context, the `cotal` toolset list and the mesh scope,
   no `discord_admin`. Its approval asks always reach the operator as once/deny cards; no configured
   bypass or earlier grant skips them. Peers write the note text whoever started the thread, so
   this reading is conservative; Matt can loosen it at merge.

## Problem / Intent

Three refusals block the use case. `_TOOLSET_PLATFORM_RESTRICTIONS` (`hermes_cli/toolset_scope.py`)
keeps `discord` off `cotal`. `_discord_message_admission` (`plugins/platforms/discord/adapter.py`)
drops the bot's own messages. `_plugin_origin_admissible` and `_dispatch_plugin_origin_injection`
refuse an origin with no `committed_route_envelope` or session, as a bot-created thread is.

## Approach

### Q1: toolset and mesh scope

Add `"cotal"` to the `discord` row. `discord` stays in `_DEFAULT_OFF_TOOLSETS`
(`hermes_cli/tools_config.py`); `discord_admin` stays Discord-only.

Peer text drives cotal turns. In `tools/discord_tool.py`, `_require_post_scope` allows any channel
when `discord.post_channels` is unset, `_create_thread` has no scope check, and `_fetch_messages`
and `_search_members` reach any channel or guild. A new `_require_mesh_scope` gives each core
action (`_CORE_ACTION_NAMES`) a rule on a peer-mesh or plugin-origin turn. The operator's guilds
come from a cached REST GET (`_discord_request`) of each `post_channels` entry, taking its
`guild_id`; no channel set is hard-coded (RIG-7938). If any entry fails or has no guild, every read
and search is refused. Writes check the list itself:

- `create_thread`, `create_forum_post`, `post_message`: the list holds the target or its thread
  parent;
- `fetch_messages`, `list_forum_posts`: the channel is not a DM or group DM (types 1 and 3) and is
  in an operator guild;
- `search_members`: the guild is an operator guild.

With no list, every core action is refused.

### Q2: plugin-origin bootstrap

A new descriptor, `plugin_origin_v1`, admits a plugin turn to a route with no human envelope.

**Trust boundary.** For a `plugin_origin_v1` row, `_plugin_origin_authorized` (new, in
`gateway/run_plugin_injection.py`) is the whole authorization. It reads only:

- `allow_gateway_injection` and the new `allow_origin_sessions` in the plugin's routed profile
  home, the allow-list that keeps other injectors out;
- the live Discord adapter `_plugin_origin_source` resolves for that profile;
- a thread origin (`chat_type == "thread"`, `thread_id == chat_id`) whose `owner_id`, read by a
  REST GET, is the bot user;
- the emergency stop (`check_paused` in `agent/estop.py`);
- that profile's `thread_sessions_per_user`, which must be false.

No human principal check runs, and no user id stands in for one. `_dispatch_plugin_origin_injection`
picks the kind first: `plugin_origin_v1` when `eligible` holds (no committed human envelope). That
kind calls `_plugin_origin_authorized` before and instead of `_plugin_injection_authorized`, whose
`_principal_authorized` path (`gateway/authz_mixin.py`) needs an allowlisted human `user_id`; other
kinds keep `_plugin_injection_authorized`. The source user is `plugin:<id>` with `is_bot: true`. The
restored event is internal, and at `c4093027` `_hm_admit_event` (`gateway/run_inbound.py`) returns
at `if is_internal:` before `_is_user_authorized_for_source` and `_hm_estop_gate`. Threads are
shared (`build_session_key`), so the operator's reply joins the session; `_threads.mark_async` lets
`_in_bot_thread` admit it without a mention.

Every injection from a plugin with `allow_origin_sessions` is scheduled in a fresh
`contextvars.Context()`: `_schedule` (`gateway/session_authority.py`) starts the drain in the
caller's context, and `_get_approval_mode` honours a bound hosted-room policy. Admission reserves
and publishes the route as `admit_native` does, then commits `{'text', 'plugin_origin_v1'}` under
principal `automation:<route>`, until the operator's first reply.

`check` runs at the drain pre-claim and at startup recovery. It repeats `_plugin_origin_authorized`,
rereading the allow-list keys and `thread_sessions_per_user` from the plugin's routed profile home
each time (a read error or `true` refuses), and checks the descriptor's shape, owner, route and
target. A refused row settles `cancelled` (`_settle_cancelled`) and never pauses the thread. The
non-LOCAL pre-claim becomes exhaustive: an unknown descriptor key raises `permission_denied`.

`restore` builds an internal event with `allow_gateway_control=False`, so `_handle_message` skips
`_hm_pending_reply_intercepts`. `execute_admission` binds a `plugin_origin_turn` marker for a
`plugin_origin_v1` row and for an automation row whose identity (`["plugin", <id>, ...]`, from
`_admit_plugin_injection`) names a plugin with the key; an unreadable config counts as set. Under
the marker, `_resolve_enabled_toolsets_for_source` (`gateway/run_turn.py`) resolves
`platform_toolsets.cotal` and `_require_mesh_scope` applies.

### How approvals reach the operator

1. The fresh context inherits nothing, so every plugin-origin row, bootstrapped or existing, binds
   its surface from the stored route: `restore` (or `restore_local_automation` for a keyed note)
   takes `live.source` from `authority.sessions`, and `_set_session_env` (`gateway/run.py`) binds
   `platform=discord` and the session key from it. `_presence` (`tools/approval.py`) gets
   `is_gateway` from that binding.
2. `TurnRunner.__init__` sets `_approval_owner` from `authority.sessions`, and
   `_run_conversation_with_approval` registers `_approval_notify_sync` for the route's session key,
   which sends the card into the thread.
3. A secret guard's `approve` with `review: true` runs `request_tool_approval` with `review_first`:
   a reviewer DENY blocks, and any other verdict asks the human.
4. `_component_check_auth` (`plugins/platforms/discord/adapter_component_auth.py`) accepts
   allowlisted users (confirmed live), allow-all, role-allowlist members and paired users. With
   `discord.approval_mentions`, the card mentions allowlisted users.
5. A plugin turn is never a viewer turn (`is_viewer_turn_with_approver`), so the whole-script
   `execute_code` gate stays.
6. On a plugin-origin turn, `_run_approval_gate`, `check_all_command_guards`,
   `check_dangerous_command` and `check_execute_code_guard` test the marker before their bypasses:
   yolo, `approvals.mode: off`, the permanent allowlist and `is_approved` grants no longer skip the
   ask. A smart-guardian approve becomes a card; a guardian deny blocks with no card. Cards offer
   once or deny and persist nothing, like a smart-DENY override in `_human_decision`. Floor blocks
   and container skips are unchanged.

## Alternatives considered

- **Synthesize a native envelope.** It mints provenance the platform never delivered.
- **Drop the committed-envelope check.** Any injecting plugin could open any chat.
- **Run the first turn outside the FIFO.** No ordering, recovery or `_approval_owner`.
- **Reuse the cron gate.** It blocks the home channel.
- **Leave mesh reads unscoped.** A peer could read the operator's DMs. Ruled: scope them (Q1).
- **Context-only bootstrap** (record the note, run no turn). Rejected: the ruling requires the
  note to wake its post now (RIG-7983 Q2).
- **A webhook author that mentions the bot.** Rejected: `allow_bots` is guild-wide.
- **Pause the FIFO on a refused plugin row.** A revoked plugin would block the operator's replies.

## Global Constraints

- One PR on `rigel-release`, at or after `c4093027`; Python 3.11 (`requires-python`) allows
  `create_task(context=...)`.
- `committed_route_envelope`, `snapshot_automation`, `_event_clause` and `check_native_route` keep
  their behavior; no plugin check passes `allow_adapter_delegation=True`.
- `tools/approval.py` changes are limited to the plugin-origin checks in Task 3.
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
def _operator_guilds(token: str, post_channels: frozenset[str]) -> frozenset[str] | None: ...  # cached
def _require_mesh_scope(token: str, action: str, *, channel_id: str, guild_id: str) -> None: ...
```

`_require_mesh_scope` returns unless `_is_peer_mesh_approval_context()` or `is_plugin_origin_turn()`
holds; `_run_discord_action` calls it after `_require_cron_scope`.

Tests: `test_discord_toolset_allowed_on_cotal_when_configured` and
`test_discord_toolset_not_implicit_on_cotal` in `tests/hermes_cli/test_tools_config.py`.
`TestMeshScope` in `tests/tools/test_discord_tool.py`, on a cotal turn and under the marker: writes
outside the list or with no list are refused; a thread whose parent is listed passes; DM reads,
other-guild reads and an other-guild `search_members` are refused; an invalid entry or one whose
GET fails refuses every read and search; Discord turns are unchanged. Docs:
`toolsets-reference.md`, `tools-reference.md`.

### Task 2: bootstrap admission

Interfaces:

```python
# gateway/run_plugin_injection.py, GatewayPluginInjectionMixin
async def _plugin_origin_authorized(self, source: SessionSource, *, plugin_id: str,
                                    plugin_home: Path, target: str) -> bool: ...
# gateway/plugin_origin_session.py (new)
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
  `eligible` and `allowed` hold; `_dispatch_plugin_origin_injection` calls `admit`. For an allowed
  plugin, `_schedule_plugin_message_injection` passes `context=contextvars.Context()` to
  `loop.create_task`, or off the loop to `loop.call_soon_threadsafe`.
- `session_authority.py`: `_claim_next` dispatches on the head's descriptor key; a refused
  `plugin_origin_v1` head returns `('refused', first)`, and `_drain` settles it outside the lock.
  In `recover_native_sessions`, a session whose queued rows are all `plugin_origin_v1` binds
  `LiveSession(available_source, route)` once `check` matches the descriptor's `route` and `target`.
- `session_ingress.py`: `execute_admission` calls `restore` for `plugin_origin_v1` and binds
  `plugin_origin_turn` when `narrowed` holds; `row_turn_author` returns `plugin:<id>`.
- `run_turn.py`: under the marker, `_resolve_enabled_toolsets_for_source` reads the `cotal` list.

Tests in a new `tests/gateway/test_plugin_origin_session.py`, with a stub Discord adapter:

- Success: one session, one `plugin_origin_v1` row, a `plugin:<id>` source with `is_bot`, a marked
  thread, and no allowlist entry for `plugin:<id>`. A cron job the turn creates and the session
  store's origin record `plugin:<id>` as the user, never the operator.
- Refusals leave no session: a missing key, a non-thread origin, a thread the bot does not own,
  `thread_sessions_per_user`, another profile, an engaged estop.
- A second note by `session_key` is admitted. An unmentioned operator reply is admitted natively.
- Revocation, `thread_sessions_per_user: true` or an unreadable config in the plugin's profile, or
  an engaged estop, before the claim, settles the row `cancelled`; the operator's reply still runs.
- An unknown descriptor head pauses with `permission_denied`.
- Restart recovery runs a queued row, and settles it after a revocation or a
  `thread_sessions_per_user` change made across the restart.
- Two origin injections racing one native message give three ordered rows in one session.
- The turn lacks `discord_admin`, and `post_message` outside the list is refused.
- In an existing session with an operator turn, an allowed plugin's note runs under the marker
  without `discord_admin`; another plugin's note runs unchanged; an unreadable config binds it.

`test_origin_injection_is_refused_up_front_until_the_chat_holds_a_human_turn` stays green.

### Task 3: approvals reach the operator

Interfaces:

```python
# tools/approval.py
plugin_origin = approval_context.is_plugin_origin_turn()
# _run_approval_gate, check_all_command_guards, check_dangerous_command, check_execute_code_guard:
# yolo, mode off, the permanent allowlist and is_approved return early only when not plugin_origin
def _smart_gate(..., human_present: bool, plugin_origin: bool = False) -> tuple[dict | None, bool]: ...
one_shot = smart_denied or plugin_origin  # _human_decision: card data, transport flags, grant()
```

A new `tests/gateway/test_plugin_origin_approval.py` runs a fixture,
`tests/gateway/fixtures/plugin_origin_approval_peer.py`, in a subprocess: a real `GatewayRunner`,
`SessionAuthority` and `AIAgent` on `ModelPeer`, a Discord adapter recording
`_send_exec_approval_prompt`, and a plugin with both keys whose `pre_tool_call` returns `approve`
with `review: true`. Cases run in manual mode unless stated:

- Reviewer approve or escalate: the card reaches the thread, mentions the operator and offers once
  and deny only; "once" runs the effect, and a stale "always" answer persists nothing.
- Reviewer deny: no card, no effect. A human deny or a timeout: no effect.
- A dangerous `terminal` command with no plugin rule gets a card.
- An injection from a cotal turn's executor thread, with a bound room policy whose mode is off,
  still gets a card, at a bootstrap and in an existing session with an operator turn.
- On a plugin-origin turn, under `--yolo`, session `/yolo`, `approvals.mode: off`, a permanent
  grant, a session grant and a permanent-allowlist match, a dangerous `terminal` command, a
  `review: true` rule and `execute_code` each still get a once/deny card.
- Smart mode on a plugin-origin turn: a guardian approve gives a once/deny card; a guardian deny
  blocks with no card and no effect.
- A `session_key` injection into an existing Discord session with an operator turn, under `--yolo`,
  `approvals.mode: off` and a permanent grant: a once/deny card reaches that thread each time.
- The operator's own turn in the same thread keeps today's bypasses.
- A `cotal` turn with a peer sender, whose plugin rule or dangerous command would ask, keeps the
  peer-mesh deny: no card in any Discord thread or to the peer, and no effect.

Docs: the origin section of `website/docs/user-guide/features/plugins.md`.

## Tasks

- [ ] Task 1: `discord` on `cotal`, the mesh scope and the plugin-origin marker.
- [ ] Task 2: `plugin_origin_v1` admission, revalidation, settlement, recovery and tests.
- [ ] Task 3: plugin-origin approval checks, end-to-end approval tests and plugin docs.

## Spec impact

No spec here changes; fork docs change as each task lists. After the repin, the operator's profile
adds `discord` to `platform_toolsets.cotal`, sets both plugin keys, and keeps
`discord.post_channels` and `discord.approval_mentions: true`.

## Ledger impact

- [DL-648](decisions/session/DL-648.md): `discord` on `cotal`, mesh-scoped.
- [DL-649](decisions/session/DL-649.md): plugin-origin sessions.

## Sources

- RIG-8011 (rulings), RIG-7938 and RIG-7983 (parent decisions).
- `RigelBuild/hermes-agent`, `rigel-release` at `c4093027`: the files and functions named above.
