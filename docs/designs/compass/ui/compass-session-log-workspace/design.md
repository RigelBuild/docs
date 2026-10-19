# Compass Session Log and agent workspace layout

Tracker: RIG-5946. Ledger: DL-627 (partly supersedes DL-158).
Depends on: RIG-5945 (one Session Log data shape end to end), RIG-5952
(header state, subagent emission), RIG-5943 (subagent sessions reach the
store).
Builds on: [message surface rendering](../compass-message-surface-rendering/design.md),
[session-trace DiffBlock](../compass-session-trace-diff.md),
[UX foundation D6](../compass-ux-foundation/design.md#d6--rendering-the-frozen-ia-surface-by-surface-composition).

## Open Questions

The design follows each recommendation. "Load-bearing" names the tasks another
option changes.

1. **RIG-5945 shape: entries or typed.** Load-bearing: T10 (T1 to T4 render
   the block model either way). Depends on: none (the compass-server record
   for RIG-5945 picks).
   - Shape 1: session-file entries everywhere. A UI adapter, `foldEntries`,
     reads `name` and `arguments` from each entry. No proto change.
   - Shape 2: typed `SessionEvent` everywhere. `SessionToolCall` gains
     `tool_name` and `args_json` before RIG-5945 persists events.
   - **Recommendation: shape 1.** Entries already carry name and arguments,
     and the OMP TUI renders them.
2. **Subagents in the log.** Load-bearing: T11. Depends on: none.
   - A. A progress row per subagent under its `task` call; a click opens
     that subagent's own Session Log.
   - B. An inline nested trace. It needs every subagent event on the wire,
     which does not exist (RIG-5943).
   - **Recommendation: A.** The progress data exists, and Matt asked for
     click-to-open (RIG-5943).
3. **Pane-size scope and store.** Load-bearing: T5. Depends on: none.
   - A. One size per pane edge, per device, in `localStorage`.
   - B. A log width per agent, per device.
   - C. Server per-user preferences (post-MVP under DL-096).
   - **Recommendation: A.** A size belongs to the screen, not the agent.
4. **Promote state.** Load-bearing: T6. Depends on: none.
   - A. `logMode: "rail" | "docked" | "full"` per view, replacing the
     store-global `logOpen`.
   - B. A second store-global boolean.
   - **Recommendation: A.** A split shows two workspaces.
5. **Where a member's Session Log opens from a DM.** Load-bearing: T9.
   Depends on: none.
   - A. A transient right-sidebar tab, `log:<agentId>`; no pin needed.
   - B. Pin the agent, then switch its fleet pane to `Session Log`.
   - **Recommendation: A.** One click, and the pin set stays the user's.
6. **Home-topic URL.** Load-bearing: T7. Depends on: none.
   - A. A route, `/agent/:agentId/topic/:topicId`; Back and deep links work
     (DL-127).
   - B. Pane-local state with no URL.
   - **Recommendation: A.**

## Problem / Intent

The Session Log prints agent text as plain text. Markdown shows raw (`**Ledger**`,
inline dash list items), thinking shows in full, a trailing `<!-- HALT: blocked … -->`
token shows as literal text, and a tool call is a one-line title with no
arguments (Matt's 10-19 review screenshot). The agent workspace is also rigid:
fixed pane widths and heights, no way to read the log full width, threads open
away from the workspace, and DM channels hide who is in them. Make the Session
Log read like an OMP coding-agent session, and make the workspace a resizable,
promotable layout that keeps an agent's work in one place.

## Approach

### A1 — One block model, keyed rows, markdown text

The renderer consumes one block model, `TraceItem` in
`apps/ui/src/session-events.ts`: `text`, `thinking`, `tool` (name, args, and
its result: status, output, diffs), `halt`, and the existing `plan` and
`notice`. Either RIG-5945 shape feeds it, through `foldSession` or
`foldEntries` (OQ-1). A1 to A4 sit above this seam and do not wait.

`foldSession` rebuilds every item on every event. Without keys, every row
remounts, re-parses its markdown, and closes any open `<details>`. So each item
gets a stable `key`, and `SessionTrace` uses `<For keyed={(item) => item.key}>`,
as `SplitPane` does.

A `text` item renders through `MarkdownText`
(`apps/ui/src/components/MarkdownText.tsx`), the message pipeline. Its
`byHandle` builder moves from `TopicView` to `comms.ts` as `handleIndex`.

### A2 — The HALT token becomes a status chip

A trailing `<!-- HALT: <kind> <argument> -->` shows as text through
`rehypeInertRaw`. The UI parses it, because under shape 1 history never passes
the agent mapper. The grammar:

- The token is the last thing in the text after trimming trailing space.
- The kind is `done`, `blocked` or `awaiting-user`; the argument is required.
- A `blocked` argument is an issue key (`ABC-123`), `owner/repo#123`, `#123`,
  or `no-issue: <reason>`.
- A token inside an open fence (```` ``` ```` or `~~~`, up to three spaces of
  indent, closed only by the same marker) is code.

A malformed token stays as text. A valid one becomes a `halt` item,
`.trace-halt[data-halt]` "kind · argument", in the `.agent-halt-dot` colours
(`awaiting-user` maps to `BoardHalt` `awaiting_user`). While the last message
streams, a trailing fragment that could still become a token (`<!--`,
`<!-- HALT: blo`) is hidden.

### A3 — Thinking is collapsed

`thinking` is a `<details class="block-thinking">` with the summary `Thinking`,
closed by default. Hiding it was rejected: the text is lost when needed.

### A4 — Every tool call shows its arguments

The row is status, title, and one inline argument line; a closed fold holds the
full arguments and output; diffs stay outside it (DiffBlock record). As in OMP,
a tool with its own renderer shows its main argument; others use the generic
line.

| Tool | Inline line |
| --- | --- |
| `bash` | `command` |
| `read`, `write`, `edit` | `path` |
| `grep` | `pattern`, then `path` |
| any other | `key=value` joined by spaces, strings quoted, nested values as `[n items]` or `{n keys}`, cut at a width budget with `…` |

The fold uses the OMP tree limits: depth 6, 200 lines, 2,000 characters per
scalar. With diffs, it shows only the main argument. The adapter drops the
intent key `i` and `__partialJson`.

### A5 — Subagents: a progress row and their own log

This record owns the layout (OQ-2 A):

- Under a `task` row, each subagent is a `button.trace-subagent`:
  `agent · description · status · current tool`.
- A click opens `/agent/:agentId/subagent/:subagentId`: the log pane shows
  that subagent's own log, with a back control. The RIG-5952 tree row opens
  the same route.
- A subagent with no matching `task` row goes in a final block.
- Grandchild subagents are dropped (DL-287: two levels).

### A6 — Resizable panes, widths and heights

Five pane edges get a handle. Sizes are device preferences (OQ-3) in
`localStorage` as `compass.settings.<key>` through `safeLocalStorage()`; a bad
value reads as the default. A drag writes once, on release.

| Key | Variable | Default | Min | Max | Handle |
| --- | --- | --- | --- | --- | --- |
| `logWidth` | `--log-w` | 340px | 240px | 960px | log start edge |
| `leftWidth` | `--left-w` | 244px | 180px | 400px | left sidebar end edge |
| `rightWidth` | `--right-w` | 400px | 280px | 720px | right sidebar start edge |
| `logHeight` | `--log-h` | 240px | 120px | 640px | stacked log top edge (container under 560px) |
| `boardHeight` | `--board-h` | natural | 48px | 480px | board strip bottom edge |

The main pane keeps a `--main-min: 320px` floor: each sidebar is
`min(var(--x-w), (100vw - var(--main-min)) / 2)`, and the log is
`min(var(--log-w), 100cqw - var(--main-min))`.

One value-generic `ResizeHandle` serves these edges and replaces the splitter
inside `SplitPane`; the window split keeps its own ratio store (DL-390). The
handle is a hit area on the `--cx-border-strong` pane edge, not a second line
(DL-437).

### A7 — Promote the Session Log

`logMode` lives in the view scope beside the workspace tabs (OQ-4), so each
window-scoped view (DL-160) owns its own; a new workspace starts `docked`. In
`full`, the log takes the body; the header and
board stay. `.av-body` stays in the DOM under `hidden`, so the draft and scroll
survive. It sets `display: flex`, so it needs `.av-body[hidden] { display: none }`,
like `.view-panel[hidden]`. The `Full`/`Dock` button (`aria-pressed`) and
`W Z` act on the focused view. Minimize goes to `rail`, and back to `docked`.

### A8 — Workspace scope

**Threads.** A home-channel topic opens inside the workspace at
`/agent/:agentId/topic/:topicId` (OQ-6), with the log beside it.
`channelPath` and `topicPath` find a home channel by `account.homeChannelId`,
not by kind: server home channels are `ChannelKindChannel` (`Store.CreateAgent`
in `go/internal/store/accounts.go`) and open as plain channels today. Old
`/channel/…/topic/…` links still work.

**Home row.** The agent tree drops the home-channel row. The agent row opens
the workspace and shows the home channel's unread count. Attached channels
(DL-344) keep their rows. This amends §5 of the
[channels-in-agent-tree record](../compass-channels-in-agent-tree/design.md).

**DMs.** A DM with agent members is labeled by its agents: `personal ↔ supervisor`
for two, comma-joined for more. U+2194 is in Space Mono and IBM Plex Mono.
Human-only DMs keep today's label. The DM header lists each agent member with
"Open workspace" (`openLink`; `Mod`+click or middle-click opens a tab, DL-390)
and "Session Log", a transient right-sidebar tab (OQ-5).

### A9 — Ledger

- **DL-627** (new) records A1 to A8.
- **DL-158**: only "fixed" changes; its status carries a DL-208-style clause
  note. The DL-153 shape (home channel plus Session Log), "no arbitrary split
  tree", and the terminal and file-pane retirement stand.

### Consumed interfaces

- **RIG-5945** (OQ-1). Shape 1: the SDK `AssistantMessage.content[]` (`text`,
  `thinking`, `toolCall { id, name, arguments, intent? }`) and
  `ToolResultMessage` (`toolCallId`, `content`, `isError`, `details`). Shape 2:
  today's `SessionEvent` plus the T10 fields. RIG-5945 owns the
  history-then-boundary tail and the store reset; this record owns the adapter
  into `TraceItem`.
- **RIG-5952** owns the header state mapping and subagent emission; T11
  consumes `SubagentRow`, from the SDK progress data.
- **RIG-5943** owns subagent sessions in the store; T11 consumes
  `store.subagentSession(agentId, subagentId): AgentSession | undefined`.

## Global Constraints

- TypeScript throughout; `moon run compass-ui:typecheck compass-ui:test` must
  pass; biome-clean.
- The standing UI rules apply (Solid v2, `--cx-*` tokens, the type grid,
  red-first `bun:test`, DL-399 baselines named in the PR body).
- **a11y.** Folds are native `<details>`. A handle is `role="separator"` with
  `aria-orientation`, `aria-valuemin`, `aria-valuemax`, `aria-valuenow`,
  `aria-label`; Arrows step, Shift+Arrow steps ×4, Home/End jump, double-click
  resets. `Full` has `aria-pressed`.
- **Docs.** Each task updates `apps/ui/src/design/surfaces.md` § "Agent
  workspace — home channel and session trace".
- **Stacking.** One linear line: T1 to T9 now, T10 after the RIG-5945 record
  freezes, T11 last.

## Plan

### T1 — Keyed block model and markdown text

- **Interfaces:**
  - Every `TraceItem` gains `key: string`: `text:<messageId>`,
    `thinking:<messageId>`, `tool:<toolCallId>`, `plan`, `notice:<eventId>`.
  - `comms.ts`:
    `export function handleIndex(accounts: readonly Account[]): Map<string, Account>`;
    `TopicView` calls it.
  - `SessionTrace: Component<{ items: TraceItem[]; byHandle: Map<string, Account>; streaming: boolean }>`,
    keyed by `item.key`. `text` renders
    `<div class="block-text"><MarkdownText text={item.text} byHandle={props.byHandle} /></div>`.
  - `TracePane` passes a `handleIndex` memo and `streaming = session.running`.
- **Tests:** `**Ledger**` gives `<strong>`, `- a` gives `<li>`; an open
  `<details>` survives a new event; `handleIndex` maps both handle forms.
- **Gate:** `agent.png`.

### T2 — HALT chip

- **Interfaces:**
  - New `apps/ui/src/halt-token.ts`:

    ```ts
    export type SessionHalt = { kind: BoardHalt["kind"]; argument: string };
    export function parseHaltToken(text: string, streaming: boolean): { body: string; halt?: SessionHalt };
    export function withHalts(items: TraceItem[], streaming: boolean): TraceItem[];
    ```

  - `TraceItem` gains `{ kind: "halt"; key: string; halt: SessionHalt }`.
    `withHalts` moves each token into a `halt` item after its `text` item;
    only the last `text` item gets `streaming`.
- **Tests:** each kind, bad kind, no argument, bad `blocked` argument, open
  and indented fences, text after the token, unterminated token streaming vs
  final.
- **Gate:** `agent.png` if the fixture holds a token.

### T3 — Collapsed thinking

- **Interfaces:** `thinking` renders
  `<details class="block-thinking"><summary>Thinking</summary><div class="thinking-body">`;
  the italic and dim rules move to `.thinking-body`.
- **Tests:** closed by default; stays open across a new event.
- **Gate:** `agent.png`.

### T4 — Tool row

- **Interfaces:**
  - The `tool` item becomes
    `{ kind: "tool"; key: string; toolCallId: string; name?: string; title?: string; args?: Record<string, unknown>; status: ToolCallStatus; output: string; diffs: FileDiff[] }`.
  - New `apps/ui/src/tool-args.ts`:

    ```ts
    export const PRIMARY_ARGS: Readonly<Record<string, readonly string[]>>;
    export function inlineArgs(name: string | undefined, args: Record<string, unknown>, maxChars: number): string;
    export function argTree(args: Record<string, unknown>, only?: readonly string[]): string[];
    ```

  - `ToolRow` order: `.tool-status`, `.tool-title`
    (`title ?? name ?? toolCallId`), `.tool-args` (`inlineArgs(name, args, 160)`),
    then `<details class="tool-output">` holding `<pre class="tool-args-body">`
    and `.tool-output-body`, then diffs. With diffs, `argTree` gets
    `only = PRIMARY_ARGS[name]`.
- **Tests:** the primary table; generic quoting, nested summaries and the `…`
  cut; tree limits; fold closed; diffs outside it.
- **Gate:** `agent.png`.

### T5 — Resizable panes

- **Interfaces:**
  - New `apps/ui/src/pane-sizes.ts`:

    ```ts
    export type PaneSizeKey = "logWidth" | "leftWidth" | "rightWidth" | "logHeight" | "boardHeight";
    export const PANE_SIZE_LIMITS: Readonly<Record<PaneSizeKey, { min: number; max: number; initial?: number }>>;
    export function loadPaneSize(storage: Storage | undefined, key: PaneSizeKey): number | undefined;
    export function savePaneSize(storage: Storage | undefined, key: PaneSizeKey, px: number | undefined): void;
    ```

  - `store.ts`: `paneSize(key): number | undefined`,
    `setPaneSize(key, px): void`, `commitPaneSize(key): void`,
    `resetPaneSize(key): void`.
  - New `components/ResizeHandle.tsx`:

    ```ts
    export type ResizeHandleProps = {
      label: string;
      orientation: "vertical" | "horizontal";
      value: () => number;
      min: number;
      max: number;
      keyStep: number;
      perPx: () => number; // signed value units per pointer px
      onInput: (value: number) => void;
      onCommit: () => void;
      onReset?: () => void;
    };
    ```

  - The splitter CSS moves from `split-pane.css` to
    `design/components/resize-handle.css`. `SplitPane` uses `ResizeHandle`
    (value = ratio, `MIN_RATIO`/`MAX_RATIO`, `KEY_STEP`, `perPx` = 1 /
    container px, `onInput` dispatches `resize`). Pane edges use
    `perPx = ±1`; an unset `boardHeight` reads the measured strip height.
  - `app.css` adds `--log-w`, `--left-w`, `--log-h`, `--board-h`,
    `--main-min` to the layout-knob `:root` block with the A6 caps; `.left`
    drops 244px.
- **Tests:** `pane-sizes` round-trip, bad value, clamp; `ResizeHandle` keys,
  one commit per drag, aria; split tests unchanged; new
  `e2e/pane-resize.spec.ts`: main pane ≥ 320px at 1024px, sidebars at max.
- **Gate:** every shot with a sidebar or log edge.

### T6 — Promote, per view

- **Interfaces:**
  - `view-scope.ts`: `export type LogMode = "rail" | "docked" | "full"`.
    `AgentWorkspace` gains `logMode` (`freshWorkspace` sets `"docked"`).
    `ViewScope` gains `logMode: Accessor<LogMode>`, `toggleLog(): void`
    (`rail` ↔ `docked`, `full` → `rail`) and `togglePromoteLog(): void`.
  - `store.ts` deletes `logOpen`, `toggleLog` and the `logResetForAgentId`
    reset.
  - `AgentView` sets `data-log` on `.agent-view` and `hidden` on `.av-body`
    in `full`; `app.css` adds `.av-body[hidden] { display: none }`.
    `LogPanel` reads `view.logMode()` and adds `button.obs-promote`.
  - `keymap.ts` adds `{ chord: "W Z", commandId: "log.togglePromote" }`;
    `spine.ts` runs `focusedView().togglePromoteLog()`.
- **Tests:** "log panel (D2)" tests move to the view scope; promoting one
  split view leaves the other docked; `W Z` bound.
- **Gate:** `agent.png`.

### T7 — Home threads inside the workspace; home row dropped

- **Interfaces:**
  - `view-route.ts`: the agent arm becomes
    `{ view: "agent"; agentId: string; topicId?: string }`; `parseRoute` and
    `routePath` handle `/agent/:agentId/topic/:topicId`. `appRoutes` and
    `ROUTE_PATTERN` gain it; `routeTitle` gives `"{topic} · @{handle}"`.
  - `view-scope.ts`: `topic` resolves on the agent arm; an unknown topic
    replaces the URL with `/agent/:agentId`.
  - `store.ts`: `selectedTopicId` reads the agent arm; `channelPath` and
    `topicPath` route a home channel through a new
    `homeAgentIdOf(channelId: string): string | undefined`.
  - `PaneView` renders `<TopicView />` when `view.topic()` is set;
    `TopicView` falls back to `view.workspaceChannel()`.
  - `stub-data.ts`: `AgentTreeNode` gains `home?: Channel`, out of
    `channels`. `LeftSidebar` `claimedIds` includes it; `AgentLeaf` shows
    `.ch-unread` from `home.unread`. Check `AgentsView.tsx`, `board.ts`.
- **Tests:** the "home-DM topic row drills into the topic on click" test
  expects the agent route, log mounted; unknown-topic fallback; home paths for
  both kinds; no home row; agent-row unread.
- **Gate:** shots with the agent tree, and `agent.png`.

### T8 — DM labels and workspace links

- **Interfaces:**
  - `dmLabel(channel, callerId, byId)` keeps its signature; with agent
    members it names them, two joined by `" ↔ "`, more by `", "`.
  - `ChannelHeader`, for a DM with agents, renders `ul.conv-members`: per
    agent, the handle and `button.conv-member-workspace`
    (`openLink(store, "/agent/<id>", inPlace)`).
- **Tests:** two agents plus owners, three agents, human-only unchanged;
  link navigates; `Mod`+click opens a tab.
- **Gate:** shots with a DM.

### T9 — Session Log in the right sidebar

- **Interfaces:**
  - `RightSidebarTab` gains `` `log:${string}` ``. `store.showAgentLog(agentId: string): void`
    opens it. `ActivityBarItem` gains
    `` { kind: "log"; id: `log:${string}`; agentId: string; letter: string } ``,
    shown while active.
  - `LogPanel.tsx` exports
    `TracePane: Component<{ session: AgentSession | undefined }>`.
    `RightSidebar` renders `.log-pane` with a head and
    `<TracePane session={store.agentSessionById(id)} />`.
  - `shownAgentIds` adds that agent while the sidebar is open.
  - `ChannelHeader` adds `button.conv-member-log`.
- **Tests:** opens with no pin and streams; another tab drops it.
- **Gate:** `right-sidebar.png`.

### T10 — Tool name and arguments source

- **Gate:** the RIG-5945 shape record frozen; build only the ruled half.
- **Shape 1:** new `apps/ui/src/session-entries.ts`:

  ```ts
  export function foldEntries(entries: readonly SessionEntry[]): TraceItem[];
  export function diffsFromResult(details: unknown): FileDiff[];
  ```

  `SessionEntry` is RIG-5945's wire type. `toolCall` maps `id`, `name`,
  `arguments` (minus `i`, `__partialJson`) and `intent` (title);
  `ToolResultMessage` maps `output`, and `failed` on `isError`.
  `diffsFromResult` ports `extractDiffs` (`packages/compass-agent/src/mapping.ts`).
- **Shape 2:** `SessionToolCall` gains `string tool_name = 4; string args_json = 5;`;
  `mapping.ts` adds `toolArgsJson(args: unknown): string` (same drops; strings
  cut at 2,000, or 60 past 16,000 total); `adapt.ts` adds
  `parseToolArgs(json: string): Record<string, unknown> | undefined`.
- **Tests:** each block kind and the hidden keys; the caps under shape 2.

### T11 — Subagent rows and their own log

- **Gate:** RIG-5952 and RIG-5943 merged.
- **Interfaces:**

  ```ts
  export type SubagentRow = { id: string; parentToolCallId?: string; agent: string; description?: string; status: "started" | "completed" | "failed" | "aborted"; currentTool?: string; currentToolArgs?: string };
  ```

  - The `tool` item gains `subagents: SubagentRow[]`; unmatched rows go in a
    final `{ kind: "subagents"; key: "subagents"; rows: SubagentRow[] }`.
  - `button.trace-subagent` opens `/agent/:agentId/subagent/:subagentId`.
    The agent arm gains `subagentId?: string`; `appRoutes` and
    `ROUTE_PATTERN` gain the route.
  - With `subagentId` set, `LogPanel` renders
    `store.subagentSession(agentId, subagentId)` and `button.obs-back`.
- **Tests:** attach by `parentToolCallId`; unmatched to the tail; grandchildren
  dropped; click routes, back returns.
- **Gate:** `agent.png` if the fixture gains a subagent.

## Tasks

- [ ] T1 — Keyed block model and markdown text
- [ ] T2 — HALT chip
- [ ] T3 — Collapsed thinking
- [ ] T4 — Tool row
- [ ] T5 — Resizable panes
- [ ] T6 — Promote, per view
- [ ] T7 — Home threads inside the workspace; home row dropped
- [ ] T8 — DM labels and workspace links
- [ ] T9 — Session Log in the right sidebar
- [ ] T10 — Tool name and arguments source (after the RIG-5945 record)
- [ ] T11 — Subagent rows and their own log (after RIG-5952, RIG-5943)
