# Compass forge: tenant-bound GitHub App installations

Tracking: RIG-5689 (work; parent RIG-5653). Ruling: Matt, 2026-10-18, on RIG-5668. Option A,
deployment-wide installations, stays for dogfood and is documented by compass#2398. Option B is
the real product: each installation is bound to a tenant, and a tenant or user installs the App
from the product.

## Open Questions

All four are load-bearing. The plan is written against each recommendation. OQ-2 depends on
OQ-1: a connect step with no tie to an install is only safe if the proof is a proof of control.

1. **OQ-1, what proves a user may bind an account.** `GET /user/installations` lists every
   installation whose repositories the user can reach. That includes an outside collaborator on
   one repository. Visibility alone would let that collaborator bind the whole installation to
   their own tenant, and its agents could then be granted write on every repository in it.
   - (a) **Recommended.** Proof of control. A user account binds only when its id equals the
     id from `GET /user`. An organization binds only when `GET /user/memberships/orgs/{org}`
     returns `state: active` and `role: admin`. Cost: the App needs the organization
     permission "Members: read", and each organization must approve it. Until it does, its
     admins read as not eligible.
   - (b) Admin on every repository. `GET /user/installations/{installation_id}/repositories`
     returns the user's permissions on each repository. Bind only when every one is `admin`.
     This needs no new App permission. But a repository added to the installation later is
     not covered, and an installation on all of a large organization's repositories costs many
     pages.
   - (c) Match the installer. The `installation` webhook (`action: created`) names the
     `sender`. Bind only when it equals the OAuth user. This covers new installs only, and only
     when the webhook lane runs.
2. **OQ-2, how connecting relates to installing.**
   - (a) **Recommended.** Connecting is its own step. "Connect GitHub account" runs the OAuth
     web flow. Compass lists the user's installations, marks those that pass OQ-1, and the user
     confirms which to bind. "Install the GitHub App" is a plain link to the install page,
     with no correlation. This covers new installs, existing installs, and organization
     installs that waited for owner approval.
   - (b) Correlate through the Setup URL. Compass appends `state` to the install link and reads
     `installation_id` and `state` at the Setup URL. That `state` is forwarded is
     **undocumented**: GitHub's docs do not say the install page passes it on. An account that
     already has the App gets no Setup URL redirect unless "Redirect on update" is on and the
     user saves a change. An install that waits for approval returns `setup_action=request`
     with no `installation_id`.
3. **OQ-3, the self-host default and migration of today's installations.**
   - (a) **Recommended.** A one-shot seed plus a default seed. On the first boot of this
     release, if the deployment has exactly one tenant, bind the account of every installation
     of either App to that tenant, and record that the seed ran. On every boot, bind the
     default installation's account (`--forge-installation-id`) to the bootstrap tenant when it
     is unbound. Dogfood needs no action, including `mattwilkinsonn`. Installs after the
     upgrade still need a connect.
   - (b) Default seed only. Grants on every other existing owner stop minting at upgrade, until
     an admin connects that owner.
   - (c) At every boot, bind every unbound installation to the bootstrap tenant. On a shared
     deployment, an account that installs before its tenant connects it lands in the bootstrap
     tenant. That is the leak this record closes.
4. **OQ-4, who may bind or unbind.**
   - (a) **Recommended.** A tenant admin (`UserRoleAdmin`). This follows
     `SetForgeMirrorSetting`. A binding widens what every user of the tenant can grant.
   - (b) Any user of the tenant. This is simpler, but one member could attach an account that
     the rest of the tenant should not reach.

## Problem / Intent

`InstallationResolver` in `go/internal/forge/installations.go` holds one deployment-wide map from
owner login to installation id. The grant check (`checkOrg`), the broker mint
(`ScopedAppMinter.MintFor`) and every forge call (`OwnerRoutedGitHub.clientFor`) resolve
through it. So a user in tenant X can grant an agent a repository under an owner that only
tenant Y installed, and `docs/self-host.md` warns operators not to share a deployment between
tenants.

This record binds each installing GitHub account to one tenant and checks that binding on every
lookup. A tenant admin connects an account from the product, with no restart and no config edit.

## Approach

### Binding model

A binding ties one GitHub account to one tenant on one forge host (DL-585). It is keyed by the
numeric account id, so a rename keeps it. It covers the account, so the primary and reviewer
Apps' installations on that account both follow it. Enterprise accounts own no repositories and
are not eligible.

`InstallationFor` checks the binding before the snapshot:

1. Look up the owner's login in ctx's tenant. No row returns `ErrNoInstallation` at once.
2. A store fault returns a lookup error that is never `ErrNoInstallation`.
3. With a row, read the snapshot. Its account id must equal the bound id.

A never-installed owner and a foreign-bound owner take the same path, with the same code, text
and timing, even while a snapshot refresh is throttled. Every caller already maps
`ErrNoInstallation`: `checkOrg` to `FAILED_PRECONDITION`, the broker to a scope rejection
(`gitCredentialScopeRejected`), and `clientFor` to `ErrInstallationLookup`. Each snapshot
refresh rewrites the stored login of every bound account, so a renamed owner resolves again
within `installationCacheTTL`.

The binding is a per-call point read under the caller's tenant. An unbind therefore holds on
every Server instance at the next lookup. A database fault fails forge calls `UNAVAILABLE` and
makes the broker serve its last token (`staleCredential`).

All four resolver construction sites take the same bindings source:
`buildRepoInstallationResolver` and `buildReviewerInstallationResolver` in
`go/server/git_credential.go`, and `buildBoardWebhookWiring` and `buildForgeWriteService` in
`go/server/serve.go`. `ScopedAppMinter.Mint` mints the default installation for `*` without the
resolver, so it calls `DefaultBound` first.

The tenant comes from `ctx` as the store reads it (`resolveTenant`). Every path to the resolver
already carries one:

- the bearer interceptor, for `GrantAgentRepository` and `CommsService.MirrorToForge`;
- runnerhub `FetchSecrets`, for `ResolveFor`;
- `runnerSessionAccountError`, for the forge tools through `Hub.RelayForgeCall`;
- `context.WithoutCancel`, which keeps values, for the comms drives (`DriveAsks`,
  `DriveAgent`) and the mirror recheck timers.

The socket and dev doors run as the bootstrap tenant. A mirror post to a foreign owner now fails,
and `mirrorFailureCode` in `go/server/forge_mirror.go` maps it to `NOT_FOUND`, matching
`mapForgeError`, in place of `INTERNAL`.

### Broker

`gitCredentialBroker` keys its maps and flights by a bare scope string, and `refreshDue` runs
with no tenant (DL-586). The tenant joins `entries`, `negative`, `served`, `last`, the
singleflight key and `generation`, so tenants never share a flight or abort each other's mints.
`refreshSnapshot` mints each key under `store.WithTenant(store.WithoutSystemRole(ctx), tenant)`,
as `lifecycleService.wakeCtx` does. The mint limiter and `backoffUntil` stay global: they guard
the App's one GitHub budget, so a heavy tenant can delay others.

Each mint re-checks the binding, so a grant row written before its owner was bound elsewhere
stops serving at the next mint. Unbind drops and revokes the tenant's tokens for that owner on
the serving instance, as `grantsChanged` does. Tokens another instance served live until expiry,
at most one hour.

### Connect flow

The flow follows OQ-1 (a) and OQ-2 (a) (DL-587). Attempts are rows, because the requests may
reach different instances, as in `compass-gateway-oauth-enrollment/design.md`.

1. `BeginForgeConnect` (tenant admin) stores an attempt: the SHA-256 of a random `state`, the
   caller's tenant and user, a PKCE verifier, and a 10-minute expiry. It returns
   `<public-url>/forge/github/start?state=<state>`.
2. `GET /forge/github/start` works once. It sets an `HttpOnly`, `SameSite=Lax` cookie holding
   the state's hash, then redirects to the host's OAuth authorize URL with the callback
   `redirect_uri` and an S256 challenge.
3. `GET /forge/github/callback` requires the cookie, so a forwarded GitHub link fails. It
   exchanges the code, reads `GET /user` and every page of `GET /user/installations`, marks
   each installation eligible by OQ-1 (a), and revokes the user token
   (`DELETE /applications/{client_id}/token`). A confirmation page names the target workspace
   and the starting user, and lists each candidate as eligible, bound here, or not eligible.
4. `POST /forge/github/confirm` requires the cookie, accepts only eligible candidates, consumes
   the attempt, and binds under the attempt's tenant.

An unknown, expired or used `state`, or a missing cookie, returns one 400 page. An account bound
elsewhere shows as "connected to another workspace".

Residual risk, accepted: an org admin who follows a start link forwarded from tenant X, and
confirms, binds the org to X. The confirmation page naming X is the only defense.

A bind is visible to a grant within one resolver miss refresh (`installationMissRefreshFloor`,
30 seconds). With a reviewer App configured, `checkOrg` also needs it on the owner. The binding
already covers it, so Settings offers "Install the reviewer App" on bindings that lack it, and
acceptance 2 holds once both are installed.

The routes are plain HTTP mounts on the network door, next to `/l/session/{id}`, with
`Cache-Control: no-store`. The callback only has to be reachable from the admin's browser, so a
LAN or loopback `--public-url` works. `ForgeInstallationService` mounts like `SecretsService`,
and its handler enforces OQ-4 (a) as `forgeMirrorSettingsService` does. For socket-only and
air-gapped deployments, `BindForgeAccount` binds an installed account by login with no proof.
It is registered only on the socket door, whose 0600 socket is the operator credential.

T3 adds the OAuth client id and client-secret name. With no client id the flow is off and its
routes are not mounted; with one, `--public-url` is required (`requirePublicURL`). App
registration needs:

- Callback URL `<public-url>/forge/github/callback`, wildcard matching off;
- "Request user authorization (OAuth) during installation" off;
- organization permission "Members: read";
- visibility "Any account".

No Setup URL is needed.

### Self-host default

The plan follows OQ-3 (a), in `reconcileInstallationSeed` beside `reconcileForgeSeed`. The seed
row makes an unbind survive restarts. If another tenant holds the default account, boot warns
and binds nothing.

### Managed boundary

This is a core capability by the test in compass `docs/concepts/self-host-and-managed.md` and
`docs/designs/compass/meta/oss-core-managed-boundary/design.md`. URLs come from deploy-time
config. Tenant creation and the hosted control surface are managed-plane concerns: named, not
designed here.

### Non-goals

- Board ingestion and the notify lane stay tenant-blind and resolve as the bootstrap tenant.
- The VCS mirror keeps the default installation, which the bootstrap tenant holds.
- The `installation` webhook is not handled. An uninstalled account drops out of the snapshot,
  and its binding stays until unbound.
- Snapshot size. "Any account" lets anyone install the App. `listInstallations` fails closed
  past 100 pages (`installationListPageLimit`). Filtering unbound installations is a follow-up.

## Alternatives considered

- Operator config from owner to tenant (RIG-5668): each tenant needs a config edit and a
  restart, failing acceptance 2.
- Device flow: needs another App setting, and its user code forwards as easily as a start link.
- Bindings cached in the snapshot: saves a read, but an unbind then waits for the TTL on other
  instances.

## Plan

### Global Constraints

- Go at the compass module's floor.
- No token, client secret, PKCE verifier, `state` or cookie value in a log, an error, argv or a
  `String()`. The user token is never stored.
- The resolver reads the tenant only from `ctx`.
- New tenant tables follow `0002_agent_config_overlays.sql` RLS. System-role access only in the
  entrypoints named below.
- No `github.com` literal outside `appAPIBase`'s host rule.
- Public repository: no private-repository names, paths or PR links anywhere.
- Each task lands its failing test first.
- Stack linearly on the RIG-5653 line: T1 to T5.
- Lanes: forge owns T1, T2 and T5, and the UI lane owns T4. T3 adds two Connect services and
  three HTTP routes, so it needs the compass-server lane's agreement before code (RIG-5689).

### T1. Binding, attempt and seed tables

Lane: forge.

Interfaces:

```sql
-- go/internal/store/migrations/0005_forge_installation_bindings.sql
CREATE TABLE forge_installation_bindings (
    forge_host    TEXT   NOT NULL,
    account_id    BIGINT NOT NULL,                -- GitHub user or organization id
    account_login TEXT   NOT NULL,                -- lowercase; rewritten on snapshot refresh
    bound_by      TEXT   REFERENCES accounts (id), -- NULL for a boot seed
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE) REFERENCES tenants (id),
    PRIMARY KEY (forge_host, account_id)          -- one tenant per account, deployment-wide
);
CREATE INDEX forge_installation_bindings_login
    ON forge_installation_bindings (tenant_id, forge_host, account_login);
CREATE TABLE forge_connect_attempts (
    state_hash    BYTEA PRIMARY KEY,              -- SHA-256 of state
    forge_host    TEXT NOT NULL,
    user_id       TEXT NOT NULL REFERENCES accounts (id),
    pkce_verifier TEXT NOT NULL,
    phase         SMALLINT NOT NULL DEFAULT 0,    -- 0 created, 1 started, 2 authorized
    github_login  TEXT NOT NULL DEFAULT '',
    candidates    JSONB NOT NULL DEFAULT '[]',
    expires_at    TIMESTAMPTZ NOT NULL,
    tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE) REFERENCES tenants (id)
);
-- Deployment-level, system role only: the one-shot seed ran for this host.
CREATE TABLE forge_installation_seeds (
    forge_host TEXT PRIMARY KEY,
    seeded_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

```go
// go/internal/store/forge_installations.go
type ForgeInstallationBinding struct {
	TenantID  TenantID
	Host      string
	AccountID int64
	Login     string
	BoundBy   AccountID // empty for a boot seed
	CreatedAt time.Time
}
type ForgeAccount struct {
	ID    int64
	Login string
}
var ErrInstallationBoundElsewhere = errors.New("store: forge account is bound to another tenant")
// Under ctx's tenant. Idempotent for the same tenant; ErrInstallationBoundElsewhere on the
// primary-key conflict with another tenant's row.
func (s *Store) BindForgeInstallation(ctx context.Context, b ForgeInstallationBinding) error
func (s *Store) ForgeBindingByLogin(ctx context.Context, host, login string) (accountID int64, err error) // ErrNotFound
func (s *Store) ListForgeInstallationBindings(ctx context.Context, host string) ([]ForgeInstallationBinding, error)
func (s *Store) UnbindForgeInstallation(ctx context.Context, host string, accountID int64) (ForgeInstallationBinding, error) // ErrNotFound

// System-role entrypoints.
func (s *Store) RefreshForgeBindingLogins(ctx context.Context, host string, accounts []ForgeAccount) error
func (s *Store) TenantCount(ctx context.Context) (int, error)
// Binds every unbound account to tenant and writes the seed row in one transaction.
// false, nil when the seed row already exists.
func (s *Store) SeedForgeInstallations(ctx context.Context, host string, tenant TenantID, accounts []ForgeAccount) (bool, error)

type ForgeConnectCandidate struct {
	AccountID int64  `json:"account_id"`
	Login     string `json:"login"`
	Type      string `json:"type"` // "User" or "Organization"
	Eligible  bool   `json:"eligible"`
	BoundHere bool   `json:"bound_here"`
	BoundElse bool   `json:"bound_elsewhere"`
}
type ForgeConnectAttempt struct {
	StateHash    []byte
	Host         string
	TenantID     TenantID
	UserID       AccountID
	PKCEVerifier string
	Phase        int16
	GitHubLogin  string
	Candidates   []ForgeConnectCandidate
	ExpiresAt    time.Time
}
func (s *Store) CreateForgeConnectAttempt(ctx context.Context, a ForgeConnectAttempt) error // ctx tenant; prunes expired rows
// System role. Each moves one phase forward; ErrNotFound when absent, expired, or in another phase.
func (s *Store) StartForgeConnectAttempt(ctx context.Context, stateHash []byte) (ForgeConnectAttempt, error)
func (s *Store) AuthorizeForgeConnectAttempt(ctx context.Context, stateHash []byte, githubLogin string, c []ForgeConnectCandidate) (ForgeConnectAttempt, error)
func (s *Store) ConsumeForgeConnectAttempt(ctx context.Context, stateHash []byte) (ForgeConnectAttempt, error) // DELETE … RETURNING
```

Tests (pgtest):

- Tenant Y cannot see X's binding, and binding X's account from Y returns
  `ErrInstallationBoundElsewhere`.
- Each attempt phase moves once, and a replayed or expired step returns `ErrNotFound`.
- `SeedForgeInstallations` runs once per host.

### T2. Resolver, minter and broker check the binding

Lane: forge. Base: T1.

Interfaces:

```go
// go/internal/forge/installations.go
var ErrUnbound = errors.New("forge: owner is not bound to this tenant")
type InstallationAccount struct {
	InstallationID, AccountID int64
	Login, Type               string
}
type InstallationBindings interface {
	// Account id bound to login in ctx's tenant; ErrUnbound when none. Any other error is a fault.
	BoundAccount(ctx context.Context, host, login string) (int64, error)
	// Called under the system role after each snapshot refresh.
	RefreshLogins(ctx context.Context, host string, accounts []InstallationAccount) error
}
// Error when bindings is nil.
func NewInstallationResolver(cfg GitHubAppConfig, bindings InstallationBindings) (*InstallationResolver, error)
// BoundAccount first: ErrUnbound -> ErrNoInstallation with no snapshot read; a fault ->
// fmt.Errorf("forge: binding lookup: %w", err). Then the snapshot; its account id must match.
func (r *InstallationResolver) InstallationFor(ctx context.Context, owner string) (int64, error)
func (r *InstallationResolver) DefaultBound(ctx context.Context) error // ErrNoInstallation when unbound
func (r *InstallationResolver) Accounts(ctx context.Context) ([]InstallationAccount, error)
// Warns once the installation list reaches 80 of installationListPageLimit pages.

// go/internal/forge/githubapp.go: Mint calls resolver.DefaultBound first. NewScopedAppMinter
// is deleted; tests use NewScopedAppMinterWithResolver.
func (s *ScopedAppMinter) Mint(ctx context.Context, repos []string, perms map[string]string) (ScopedToken, error)

// go/server/git_credential.go
type gitCredentialCacheKey struct {
	tenant store.TenantID
	key    string // "owner/a,owner/b" or "*"
}
// entries, negative: map[gitCredentialCacheKey]…
// served: map[store.AccountID]map[gitCredentialCacheKey]struct{}; last likewise.
// generation: map[store.TenantID]uint64. Singleflight key: string(tenant) + "\x00" + key.
func (b *gitCredentialBroker) ownerUnbound(ctx context.Context, owner string) // drops and revokes ctx tenant's entries for owner, then signals

// go/server: adapts *store.Store; passed at all four resolver construction sites.
type installationBindings struct{ st *store.Store }
func reconcileInstallationSeed(ctx context.Context, st *store.Store, primary, reviewer *forge.InstallationResolver, host string) error
```

Tests:

- From X, granting Y's owner and granting a never-installed owner return the same code and
  message with zero GitHub requests, including while a refresh is throttled.
- A binding-store fault fails the grant `UNAVAILABLE`, and the broker serves its last token.
- A pre-existing grant in X on Y's owner mints nothing at serve or refresh.
- `*` mints only in the tenant holding the default owner.
- Two tenants minting `*` at once get their own tokens, and X's grant change does not discard
  Y's mint.
- X's background refresh succeeds under X.
- A forge tool call, `MirrorToForge` and a comms drive on Y's owner from X fail.
- A renamed bound owner resolves after the next snapshot refresh.
- The one-shot seed binds every account with one tenant and only the default with two. A
  restart after an unbind leaves it unbound.

### T3. Connect flow: config, Connect services, HTTP routes

Lane: forge, with the compass-server lane. Base: T2.

Interfaces:

```proto
// proto/compass/v1/compass.proto
service ForgeInstallationService {
  // Tenant admin. FAILED_PRECONDITION when the connect flow is not configured.
  rpc BeginForgeConnect(BeginForgeConnectRequest) returns (BeginForgeConnectResponse);
  // Any authenticated user of the tenant.
  rpc ListForgeInstallations(ListForgeInstallationsRequest) returns (ListForgeInstallationsResponse);
  // Tenant admin. NOT_FOUND when the tenant holds no such account.
  rpc UnbindForgeInstallation(UnbindForgeInstallationRequest) returns (UnbindForgeInstallationResponse);
}
message BeginForgeConnectRequest {}
message BeginForgeConnectResponse { string connect_url = 1; }
message ListForgeInstallationsRequest {}
message ForgeInstallation {
  int64 account_id = 1;
  string account_login = 2;
  string bound_by_handle = 3;
  int64 created_at_unix_ms = 4;
  bool reviewer_installed = 5;
}
message ListForgeInstallationsResponse {
  repeated ForgeInstallation installations = 1;
  bool can_edit = 2;
  bool connect_configured = 3;
  string install_url = 4;          // <html_url>/installations/new for the primary App
  string reviewer_install_url = 5; // empty when no reviewer App is configured
}
message UnbindForgeInstallationRequest { int64 account_id = 1; }
message UnbindForgeInstallationResponse {}

// Socket door only.
service ForgeOperatorService {
  // Binds an installed account to the caller's tenant without a proof.
  // NOT_FOUND when no installation has that login; FAILED_PRECONDITION when bound elsewhere.
  rpc BindForgeAccount(BindForgeAccountRequest) returns (BindForgeAccountResponse);
}
message BindForgeAccountRequest { string account_login = 1; }
message BindForgeAccountResponse { int64 account_id = 1; }
```

```go
// go/server/serve.go, ForgeAppConfig
ClientID         string // --forge-app-client-id ($COMPASS_FORGE_APP_CLIENT_ID)
ClientSecretName string // --forge-app-client-secret ($COMPASS_FORGE_APP_CLIENT_SECRET); declared server_only secret NAME

// go/server/forge_connect.go (new)
// GET /forge/github/start, GET /forge/github/callback, POST /forge/github/confirm.
// nil when ClientID is empty.
func newForgeConnectHandler(st *store.Store, resolver *forge.InstallationResolver, gh forgeOAuthClient, base string, log *slog.Logger) http.Handler
type forgeOAuthClient interface {
	Exchange(ctx context.Context, code, verifier, redirectURI string) (userToken string, err error)
	User(ctx context.Context, userToken string) (id int64, login string, err error)
	UserInstallations(ctx context.Context, userToken string) ([]forge.InstallationAccount, error) // all pages
	OrgAdmin(ctx context.Context, userToken, org string) (bool, error) // state active and role admin
	Revoke(ctx context.Context, userToken string) error               // DELETE /applications/{client_id}/token
}
// go/server/network_door.go: buildNetworkServer gains forgeConnectHandler http.Handler.
// go/server/serve.go: buildDoors registers ForgeOperatorService on udsMux only.
// go/internal/auth/admin_gate.go: ForgeInstallationService procedures authenticatedOpen;
// BindForgeAccount adminOnly.
```

Tests:

- A collaborator who is not an org admin sees the org as not eligible, and a confirm naming it
  binds nothing. An Enterprise account is not eligible.
- The authorize URL without the start cookie, a replayed start link, and unknown, expired or
  used states all return the 400 page.
- The user token is revoked after the callback.
- The confirmation page names the tenant and the starting user.
- A member calling `BeginForgeConnect` gets `PERMISSION_DENIED`.
- Boot fails with a client id and no `--public-url`.
- `BindForgeAccount` is absent from the network door.
- End to end against a fake GitHub: begin, start, callback, confirm, install the reviewer App,
  then a grant on the new owner succeeds with no restart.

### T4. Settings section

Lane: UI. Base: T3.

Add `"github"` ("GitHub") to `SETTINGS_SECTIONS` in `apps/ui/src/view-route.ts` and a
`GitHubInstallationsSection` to `SECTION_VIEW`, following `ForgeMirrorSection`. It renders
`ListForgeInstallationsResponse`: connect, install, reviewer-install and disconnect controls,
gated by `can_edit` and `connect_configured`, opening URLs in the system browser and reloading
on window focus. Tests: bun component tests for the ready, empty, error and member states.

### T5. Spec, self-host doc, proof

Lane: forge. Base: T4.

Add this requirement under `## Agent sessions` in `docs/specs/product/compass.md`:

```markdown
### Requirement: GitHub App installations are bound to one tenant

The Server SHALL bind each GitHub account that installs the App to one tenant. A grant, a mint,
or a forge call for an owner whose account is not bound to the caller's tenant SHALL fail as if
the owner had no installation. A tenant admin SHALL bind an account they control from the
product without a restart or a configuration change.

#### Scenario: Owner bound to another tenant

- **Given** owner B's account is bound to tenant Y
- **When** a user in tenant X grants an agent `B/repo`
- **Then** the grant fails with the same `FAILED_PRECONDITION` as for an owner with no installation

#### Scenario: Tenant connects an account from the product

- **Given** an admin of org C, who is a tenant admin in tenant X, installs the App on C
- **When** they connect C from Settings and a user in tenant X then grants an agent `C/repo`
- **Then** the grant succeeds within one refresh

#### Scenario: Collaborator cannot connect an organization

- **Given** a user who only collaborates on one repository of org D
- **When** they connect from Settings
- **Then** org D is listed as not eligible and is not bound
```

In `docs/self-host.md`, delete the paragraph that begins "App installations are
deployment-wide". In its place, document the binding, the upgrade seed, the App registration
settings and flags from Connect flow, `BindForgeAccount`, and unbinding.

Proof on dev, in the PR body: both dev owners mint after the upgrade with no action. A second
tenant seeded in the dev database connects a test account and grants its repository, while the
same grant from the bootstrap tenant fails with the no-installation error.

## Tasks

- [ ] T1. Binding, attempt and seed tables (forge)
- [ ] T2. Resolver, minter and broker check the binding; boot seeds (forge, DL-585, DL-586)
- [ ] T3. Connect flow: config, two Connect services, three routes (forge with compass-server,
  DL-587)
- [ ] T4. GitHub settings section (UI)
- [ ] T5. Spec, `docs/self-host.md`, dev proof (forge)
