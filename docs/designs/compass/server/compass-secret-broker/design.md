# Compass Secret Broker

## Open Questions

Pending Matt's ruling on RIG-6072. Each recommendation is the working assumption this design uses.

1. **Which value backend and tenant placement should v1 use?** Option A adds OpenBao now, with a central policy boundary but extra deployment and credential-policy work before a dynamic-secret consumer exists. Option B uses shared tenant-isolated Postgres and Compass encryption. Option C uses a separate Postgres database per tenant and multiplies operations. **Recommendation: use shared encrypted Postgres in v1 behind `SecretBackend`; reject per-tenant databases. Keep OpenBao as the preferred future backend when a real dynamic-secret consumer needs it.**
2. **Which grant modes and limits should v1 support?** Option A supports once and session grants. Option B also supports standing grants, reducing repeat prompts but retaining authority longer. **Recommendation: support once, session, and standing; default mode to once and expiry to the lesser of requested TTL and its mode cap. If requested TTL is omitted, use 15 minutes.** Once expires at most 15 minutes after approval; session grants bind to one live binding version and expire at most 8 hours after approval; standing grants expire at most 7 days after approval and require origin-bound uses. A failed backend read is audited and does not consume a once grant.
3. **Where should users approve requests?** Option A reuses asks, which are not permission prompts. Option B adds a separate event stream or caller filters to the shared event bus, adding replay or authorization complexity. Option C uses Settings and request-list polling. **Recommendation: add a private Settings panel and poll `ListSecretRequests` while it is open. Avoid a shared-bus cue because `SubscribeEvents` is global and caller filtering is unnecessary complexity for this approval surface.**
4. **How should broker values be stored and delivered?** Option A puts them in A9 and adds a filter to container materialization, but every existing delivery path must remember to enforce it. Option B gives them a separate table and tool-only delivery path, at the cost of a second value namespace. **Recommendation: store broker values in `broker_secrets`; forbid FILE/ENV materialization and redeem only through trusted tools. Keep existing A9 FILE/ENV behavior unchanged.**
5. **Who receives redeemed bytes?** Option A exposes bytes to agent-readable code, which is simpler but lets compromised agent code copy them. Option B returns them only to trusted tool code, which constrains consumers to supported tools. **Recommendation: only the trusted browser runtime (RIG-6007) receives bytes: a Runner-launched controller running as a separate browser UID, reachable only over a browser-UID-only broker socket. The model and agent container receive the fill result, not the value.**
6. **Should v1 issue dynamic credentials?** Option A adds credential issuers and revocation behavior now, before a concrete consumer exists. Option B starts with human-provided static values, which leaves rotation to the user. **Recommendation: defer dynamic issuance, including GitHub App installation tokens. Add no dynamic-backend interface in v1; prefer OpenBao for a future design with a concrete dynamic-secret consumer.**
7. **Which secret scopes should v1 support?** Option A includes TENANT, USER, and AGENT, but needs agent-scope ownership and value-entry rules. Option B supports only TENANT and USER, which keeps ownership aligned to existing account scopes. **Recommendation: support TENANT and USER only; reject AGENT refs at request and value-write boundaries. Unspecified scope maps to USER.**
8. **Where should browser isolation be enforced?** Option A makes the broker responsible for browser controls it cannot operate. Option B makes browser isolation a prerequisite, adding a dependency on RIG-6007. **Recommendation: RIG-6007 owns and proves that the filled browser is outside agent code-execution reach; exposes no agent-facing eval or filled-field DOM/value reads; and blocks or redacts screenshots and accessibility-tree reads that could show filled fields (RIG-6007 blocks them after taint). The broker requires this before enabling browser fill.**
9. **Should v1 add an ACL before user approval?** Option A adds policy administration and another authorization system. Option B requires approval for every request, increasing user effort but keeping one clear authority gate. **Recommendation: defer ACLs. Approval is the sole v1 authority gate; add no ACL tables or APIs.**

## Problem / Intent

Compass stores user values encrypted in Postgres and materializes A9 values into agent containers. Agents need selected browser and vendor credentials without exposing reusable values to model context or general agent code. Add requests, user approval, scoped grants, and trusted tool redemption. The existing `secrets` tenant-key issue is out of scope and tracked separately as RIG-6070.

## Approach

### Existing contract and code

The frozen A9 record defines identity and precedence: `SecretRecordsForAgent` returns one value per name, with `agent > user > tenant` (`docs/designs/compass/server/compass-user-secret-store.md`, A9: “The injected environment holds ONE value per name”). It fixes AES-256-GCM and `value_ciphertext`, `value_nonce`, and `key_version`. DL-350 remains Active for user-managed values; DL-351, DL-355, and DL-362 govern encryption and key custody. DL-356 keeps boot/server secrets read-only. DL-639 adds a separate broker-value class and changes none of those contracts.

The code stores encrypted values in `secrets` (`go/internal/store/migrations/0001_init.sql`, `CREATE TABLE secrets`: “PRIMARY KEY (name, scope_kind, scope_id)”). `StoreResolver.ResolveFor` selects A9 values using “agent > user > tenant” (`go/internal/secrets/store_resolver.go`, `ResolveFor`). `Handler.FetchSecrets` returns live values to the Runner (“response carries live secret values”; `go/internal/runnerhub/handler.go`), and `agentHost.Start` installs them before execution (“Materialize the agent's secrets BEFORE exec'ing”; `go/internal/runner/host.go`). `SecretDelivery` supports FILE and ENV (`proto/compass/v1/compass.proto`, `SecretDelivery`: “0600 file” and “aggregate 0600 env file”). Broker values remain outside this path and cannot enter container materialization.

`BearerInterceptor` resolves the bearer token and attaches the account to request context (`go/internal/auth/doc.go`, `BearerInterceptor`: “attaches the caller's account to the request context”). Runner relays resolve the account from the Server session binding (`proto/compass/v1/runner.proto`, `RelayCommsCall`). Agent identity, owner, and tenant come from that trusted context, not request fields. An agent request is made for a durable agent account, not a temporary subagent: “A subagent has no Compass handle or account” (`docs/concepts/agent-roles.md`). The existing secret contract says “A human provides the VALUE for that named slot” (`docs/concepts/no-human-clicks.md`).

Compass asks are not permission prompts (`proto/compass/v1/comms.proto`, `Ask`: “This is not a permission prompt”). DL-054 limits notifications v1 to chat pings and asks. Use a dedicated Settings panel and poll `ListSecretRequests` while it is open. `SubscribeEventsResponse` is a global event stream (`proto/compass/v1/compass.proto`, `SubscribeEventsResponse`: “One entry in the server event stream”); the handler subscribes with `s.bus.Subscribe(req.GetSinceSeq(), req.GetInstanceEpoch())` (`go/server/service.go`, `SubscribeEvents`). Do not publish broker cues on that unfiltered bus.

### Ref, request, grant, and approval

`SecretRef` is `{scope, name}` using the existing `SecretScope` USER/TENANT enum. Unspecified scope maps to USER. The Server derives USER `scope_id` from the requesting agent's stored owner; TENANT uses an empty id. Map wire scopes explicitly to broker values (`TENANT=0`, `USER=1`); never cast the wire enum directly. Reject AGENT and unknown scopes. The broker namespace never reads, shadows, or falls back to an A9 row.

For broker-value writes, the owning user may set or delete USER refs. Only a tenant admin may set or delete TENANT refs. The Server derives the coordinate; clients cannot provide an owner id or tenant id.

A request has an exact ref, purpose, requested TTL, and typed uses. V1 supports only `browser_fill`; its binding contains proposed HTTPS origins only, normalized to scheme, ASCII/punycode host, and port with no path or wildcard. Reject opaque origins. Validate origins at request creation, approval, and redemption. An approver may narrow, but not expand, the requested use or origin set. The approver chooses mode and expiry. Mode defaults to once. Grant expiry cannot exceed the lesser of requested TTL and the mode cap: 15 minutes for once, 8 hours for session, and 7 days for standing. If requested TTL is omitted, use 15 minutes. Validate expiry against server time. USER requests go to the owning user; at review, require approver, stored request owner, current agent owner, and USER `scope_id` to be identical. TENANT requests go to a tenant admin, even when the agent has an owner. Refuse creation if the active binding has an empty version. Persist before approval polling can observe the request. `ListSecretRequests` returns only requests the caller may approve.

A grant is `Grant{grant_id, ref, agent_account_id, approver_user_id, purpose, mode, expires_at, uses[]}`. The grant id identifies a grant but does not authorize by possession alone; each use is checked against the authenticated identity and live binding. Every redemption requires a live binding for the requesting agent. Only session grants also require its `binding_version` to match the grant. Once allows one successful redemption. Session and standing allow redemption until expiry or revocation. Failed backend reads are audited and leave once grants available. Session grants bind to `(session_id, binding_version)`; `releaseSession` is eager cleanup, not the authority check. USER grants require `scope_id`, approver, and current agent owner to remain identical; TENANT grants require the approver to remain a tenant admin. On agent deletion revoke grants; on owner change cancel pending requests and revoke grants; on approver authority loss revoke grants, including TENANT admin demotion. A logical session id can persist across authorized resume (`docs/designs/compass/agent/runner-session-id-allocation/design.md`: “A resume reuses the already-authorized logical `resume_session_id`”), so it is not sufficient by itself. Standing grants are agent-bound and require origin-bound uses.

An agent calls `GetSecretRequest(request_id)` to poll its own request until it is approved, denied, cancelled, or revoked. The Server resolves the agent from the active binding and returns state, grant id, and expiry only; it never returns a value. Use bounded backoff. The agent may cancel its own pending request; only an authorized user may deny it with `ReviewSecretRequest`. A conditional state transition allows only one decision. Approval returns a grant id that identifies, but does not authorize by possession alone; every use checks the authenticated identity and live binding.

### Trusted browser runtime path

The trusted browser runtime from RIG-6007 is the only consumer. The Runner launches it per agent session as a separate browser UID, outside agent code-execution reach. The agent socket carries only request, status, and cancel calls; the Runner rejects `fill` there. The controller sends `fill` over a browser-UID-only broker socket, reports the origin from its own CDP state, and the Runner relays both with `RelaySecretCall`. The controller fills through CDP and returns only success or a `SecretCallError` to the model. Values never enter the agent socket, model arguments, prompts, tool output, or transcript. Browser fill is gated on the RIG-6007 isolation preconditions below.

### Value backend

Store requests, grants, audit rows, and broker values in shared tenant-isolated Compass Postgres. V1 implements `SecretBackend` over Postgres encrypted by existing `envelope.Key`, the operator-seeded `COMPASS_MASTER_KEY`, and row-bound AAD derived from tenant, scope, name, and key version. Values live only in `broker_secrets`; ciphertext and nonce are non-null. The interface permits a future backend without shipping an OpenBao adapter in v1.

OpenBao's public documentation says “Policies are deny by default” (`https://openbao.org/docs/concepts/policies/`). Its lease documentation says “All dynamic secrets in OpenBao are required to have a lease” and “The Key/Value Backend ... does not issue leases” (`https://openbao.org/docs/concepts/lease/`). OpenBao is therefore a candidate for a future dynamic-credential backend, not the v1 store. The core supports both self-hosted and managed deployments (`docs/concepts/self-host-and-managed.md`); this design chooses no deployment-specific topology.

## Alternatives considered

- **OpenBao in v1:** deferred until a real consumer needs dynamic credentials. V1 has static user-provided values and can use Compass encryption.
- **Per-tenant Postgres:** rejected because separate databases multiply operations without changing the v1 authorization model.
- **A9 storage:** rejected. A separate table follows the `server_secrets` precedent in `go/internal/store/migrations/0001_init.sql`, where separation is enforced “by construction rather than by a filter someone can forget.” Broker values cannot shadow or materialize with A9 values.
- **ACL before approval:** deferred. Per-request approval gives one clear authority decision; ACL adds policy administration without removing that gate.
- **Ask or event cue:** rejected. Asks are not permission prompts, and the global event bus has no per-caller filter. Poll the caller-authorized request list while Settings is open; this needs no event replay or filtering mechanism.
- **Model-readable redemption or shared browser:** rejected because model-written code could read the credential or the filled field.
- **Agent-scoped requests:** rejected because v1 has no agent-scope value-entry contract or consumer.

## Threat model

Treat model-written code and each agent as potentially compromised, matching `docs/concepts/isolation-and-egress.md`: “treat every agent as potentially compromised and contain it.” The broker denies by default. It resolves identity from verified Runner authentication and active binding, checks tenant and approver authority, validates the ref and approved use on each redemption, and audits attempts without values. Forced tenant RLS applies to every broker table. A request or grant in one tenant cannot authorize another tenant.

Only the trusted browser runtime receives bytes. It obtains origin from its own CDP state and reports it; the Server normalizes it and matches it exactly against the approved use. At fill time the runtime checks the target frame origin again and confirms navigation and frame identity are unchanged. The Server cannot see the page, so origin integrity depends on the browser-UID boundary. A compromised Server, Runner host, or browser runtime can observe values. A compromised agent must not reach the filled browser, the broker socket, or filled DOM/value state; RIG-6007 also blocks or redacts screenshots and accessibility-tree reads of filled fields. Revocation blocks future reads but cannot recall a static value already read. Users must rotate exposed downstream credentials.

Values MUST NOT appear in logs, audit, events, UI, errors, model-visible output, or persisted grants. Secret-bearing Go values implement redacted `String() string`, `GoString() string`, and `LogValue() slog.Value`, following `go/internal/envelope/envelope.go`, `Key`: “String/GoString/LogValue close the fmt and slog reflection vectors.” Protobuf `debug_redact` is metadata, not runtime protection; handlers MUST NOT log request messages. Audit records ids, use kind, canonical binding, outcome, and time, never values.

## Plan

### Global Constraints

- Keep DL-350 Active. Preserve the A9 table, key, AAD, precedence, FILE/ENV delivery, and boot/server-secret separation (DL-350, DL-351, DL-355, DL-356, DL-362).
- Broker values live only in `broker_secrets`; they never shadow A9, enter `FetchSecrets`, or materialize into containers.
- Use forced tenant RLS on every new table. Account/agent-account FK hardening is out of scope; the separate `secrets` tenant-key fix is tracked as RIG-6070.
- V1 has no ACL, OpenBao adapter, dynamic-secret interface, tombstone, backend job, or external-backend retry protocol. On owner change, cancel pending requests and revoke grants; on agent deletion revoke grants; on approver authority loss revoke grants, including TENANT admin demotion.
- Every redemption requires a live binding for the requesting agent; only session grants also require its `binding_version` to match the grant. `Hub.releaseSession` and runner binding reaping eagerly revoke matching session grants but are not authorization checks. Once grants default to 15 minutes, session grants cannot exceed 8 hours, and standing grants cannot exceed 7 days and require origin-bound uses. All expiries also respect requested TTL.
- Only the trusted browser runtime may read values, and only over the browser-UID-only broker socket. The Runner rejects `fill` from the agent socket. Do not enable browser fill until the RIG-6007 isolation precondition holds.

### T1 — Broker store and migration

Add `go/internal/store/migrations/0005_secret_broker.sql` after `0004_forge_write_intents.sql`. Follow its per-table pattern: grant `SELECT, INSERT, UPDATE, DELETE` to `compass_app` and `compass_system`, enable and force RLS, and create a `tenant_isolation` policy using the tenant GUC. Apply that grant and policy to every new table. Do not alter `secrets`, `server_secrets`, or their migrations. Map broker scope values as `TENANT=0`, `USER=1`.

`Interfaces:` `SecretCoordinate` is derived by the Server from the public ref and authenticated owner's account id. The store uses its declared `store.TenantID` (`go/internal/store/types.go`, `TenantID`). Broker AAD is exactly `"compass/broker-secret/v1\x00" + tenantID + "\x00" + strconv.FormatInt(int64(scopeKind), 10) + "\x00" + scopeID + "\x00" + name + "\x00" + strconv.FormatInt(int64(keyVersion), 10)`, matching DL-362. Bind every field, using an empty scope id for TENANT, and reject NUL in every field.

```go
type SecretCoordinate struct {
    ScopeKind int16
    ScopeID string
    Name string
}

func BrokerSecretAAD(tenantID string, coord SecretCoordinate, keyVersion int16) ([]byte, error)

type SecretBackend interface {
    Put(context.Context, store.TenantID, SecretCoordinate, []byte) error
    Get(context.Context, store.TenantID, SecretCoordinate) (SecretValue, error)
    Delete(context.Context, store.TenantID, SecretCoordinate) error
}

type SecretValue struct { value []byte }
func (SecretValue) String() string
func (SecretValue) GoString() string
func (SecretValue) LogValue() slog.Value
func (SecretValue) Bytes() []byte
```

Minimum SQL contracts (all tables include `tenant_id`, use forced tenant RLS, and have the privileges and policy above):

```sql
CREATE TABLE broker_secrets (
  tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE), scope_kind SMALLINT NOT NULL CHECK (scope_kind IN (0,1)),
  scope_id TEXT NOT NULL, name TEXT NOT NULL,
  value_ciphertext BYTEA NOT NULL, value_nonce BYTEA NOT NULL,
  key_version SMALLINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope_kind, scope_id, name),
  CHECK ((scope_kind = 0 AND scope_id = '') OR (scope_kind = 1 AND scope_id <> ''))
);
CREATE TABLE secret_requests (
  tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE), request_id TEXT NOT NULL, agent_account_id TEXT NOT NULL,
  owner_user_id TEXT NOT NULL, session_id TEXT NOT NULL, binding_version TEXT NOT NULL CHECK (binding_version <> ''),
  scope_kind SMALLINT NOT NULL CHECK (scope_kind IN (0,1)), scope_id TEXT NOT NULL,
  name TEXT NOT NULL, purpose TEXT NOT NULL,
  requested_ttl_seconds BIGINT NOT NULL CHECK (requested_ttl_seconds > 0),
  state TEXT NOT NULL CHECK (state IN ('pending','approved','denied','cancelled','revoked')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), decided_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, request_id),
  UNIQUE (tenant_id, request_id, agent_account_id, scope_kind, scope_id, name),
  CHECK ((scope_kind = 0 AND scope_id = '') OR (scope_kind = 1 AND scope_id <> ''))
);
CREATE TABLE secret_request_uses (
  tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE), request_id TEXT NOT NULL, use_id TEXT NOT NULL,
  use_kind TEXT NOT NULL, binding JSONB NOT NULL,
  PRIMARY KEY (tenant_id, request_id, use_id),
  FOREIGN KEY (tenant_id, request_id) REFERENCES secret_requests(tenant_id, request_id) ON DELETE CASCADE
);
CREATE TABLE secret_grants (
  tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE), grant_id TEXT NOT NULL, request_id TEXT NOT NULL,
  approver_user_id TEXT NOT NULL, agent_account_id TEXT NOT NULL, scope_kind SMALLINT NOT NULL CHECK (scope_kind IN (0,1)),
  scope_id TEXT NOT NULL, name TEXT NOT NULL, purpose TEXT NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('once','session','standing')),
  session_id TEXT, binding_version TEXT CHECK (binding_version <> ''), created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, grant_id), UNIQUE (tenant_id, request_id),
  UNIQUE (tenant_id, grant_id, agent_account_id, scope_kind, scope_id, name),
  FOREIGN KEY (tenant_id, request_id, agent_account_id, scope_kind, scope_id, name)
    REFERENCES secret_requests(tenant_id, request_id, agent_account_id, scope_kind, scope_id, name),
  CHECK ((mode = 'session' AND session_id IS NOT NULL AND binding_version IS NOT NULL)
      OR (mode <> 'session' AND session_id IS NULL AND binding_version IS NULL)),
  CHECK ((scope_kind = 0 AND scope_id = '') OR (scope_kind = 1 AND scope_id <> ''))
);
CREATE TABLE secret_grant_uses (
  tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE), grant_id TEXT NOT NULL, use_id TEXT NOT NULL,
  use_kind TEXT NOT NULL, binding JSONB NOT NULL,
  PRIMARY KEY (tenant_id, grant_id, use_id),
  FOREIGN KEY (tenant_id, grant_id) REFERENCES secret_grants(tenant_id, grant_id) ON DELETE CASCADE
);
CREATE TABLE secret_audit (
  tenant_id TEXT NOT NULL DEFAULT current_setting('compass.tenant_id', TRUE), audit_id BIGINT GENERATED ALWAYS AS IDENTITY,
  request_id TEXT, grant_id TEXT, agent_account_id TEXT, session_id TEXT,
  use_kind TEXT NOT NULL, binding JSONB NOT NULL, outcome TEXT NOT NULL,
  attempted_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id, audit_id)
);
```

Requests may exist before values. Deleting a broker value or revoking a grant sets its request state to `revoked`, retains request and audit history, and blocks future use. Revoke grants when an agent is deleted or an approver loses authority; on owner change, cancel pending requests and revoke grants. TENANT approver demotion revokes grants approved by that user. Store the approving user id on each grant so current authority can be checked at redemption. The backend derives AAD from tenant, scope, name, and key version.

The migration uses this exact grant and RLS pattern for each new table, as in `0004_forge_write_intents.sql`:

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON broker_secrets TO compass_app, compass_system;
ALTER TABLE broker_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE broker_secrets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON broker_secrets
  USING ((SELECT current_setting('compass.tenant_id', TRUE)) <> ''
         AND tenant_id = (SELECT current_setting('compass.tenant_id', TRUE)))
  WITH CHECK ((SELECT current_setting('compass.tenant_id', TRUE)) <> ''
         AND tenant_id = (SELECT current_setting('compass.tenant_id', TRUE)));
```

Repeat those statements for `secret_requests`, `secret_request_uses`, `secret_grants`, `secret_grant_uses`, and `secret_audit`.

### T2 — Request, approval, and grant API

Add broker-value set/delete and user-authenticated request list/review and grant revocation APIs to `SecretsService`. Only the current USER owner or a tenant admin for TENANT grants may call `RevokeSecretGrant`. At review of a USER request, require the authenticated approver, stored request owner, current agent owner, and USER ref scope id to be identical. On owner change, cancel pending requests and revoke grants; on agent deletion revoke grants; on approver authority loss revoke grants, including TENANT admin demotion. Reject empty binding versions at request creation. Add request creation, status, and agent cancellation operations to one AgentGateway `Secret` call family, relayed by one RunnerService RPC. Approval and grant creation are one transaction. User APIs derive caller and tenant from authentication. Use the existing `SecretScope` enum and append new RPCs and messages; do not change A9 messages.
`Interfaces:`

```proto
rpc SetBrokerSecret(SetBrokerSecretRequest) returns (SetBrokerSecretResponse);
rpc DeleteBrokerSecret(DeleteBrokerSecretRequest) returns (DeleteBrokerSecretResponse);
rpc ListSecretRequests(ListSecretRequestsRequest) returns (ListSecretRequestsResponse);
rpc ReviewSecretRequest(ReviewSecretRequestRequest) returns (ReviewSecretRequestResponse);
rpc RevokeSecretGrant(RevokeSecretGrantRequest) returns (RevokeSecretGrantResponse);

message SecretRef { SecretScope scope = 1; string name = 2; }
message SecretUse { string use_id = 1; SecretUseKind kind = 2; SecretUseBinding binding = 3; }
message SecretUseBinding { repeated string allowed_origins = 1; }
enum SecretUseKind { SECRET_USE_KIND_UNSPECIFIED = 0; SECRET_USE_KIND_BROWSER_FILL = 1; }
message SetBrokerSecretRequest { SecretRef ref = 1; string value = 2 [debug_redact = true]; }
message SetBrokerSecretResponse {}
message DeleteBrokerSecretRequest { SecretRef ref = 1; }
message DeleteBrokerSecretResponse {}
enum GrantMode { GRANT_MODE_UNSPECIFIED = 0; GRANT_MODE_ONCE = 1; GRANT_MODE_SESSION = 2; GRANT_MODE_STANDING = 3; }
message RequestSecretRequest { SecretRef ref = 1; string purpose = 2; int64 requested_ttl_seconds = 3; repeated SecretUse uses = 4; }
message ReviewSecretRequestRequest { string request_id = 1; bool approved = 2; GrantMode mode = 3; int64 expires_at_unix_ms = 4; repeated SecretUse uses = 5; }
message ReviewSecretRequestResponse { string grant_id = 1; int64 expires_at_unix_ms = 2; }
message RevokeSecretGrantRequest { string grant_id = 1; }
message RevokeSecretGrantResponse { bool revoked = 1; }
message ListSecretRequestsRequest {}
message ListSecretRequestsResponse { repeated SecretRequest requests = 1; }
message SecretRequest { string request_id = 1; SecretRef ref = 2; string purpose = 3; int64 requested_ttl_seconds = 4; repeated SecretUse uses = 5; string state = 6; string grant_id = 7; int64 expires_at_unix_ms = 8; }
```

```proto
rpc Secret(AgentSecretCallRequest) returns (SecretCallResult); // AgentGateway, agent socket: no fill
rpc FillSecret(BrokerFillRequest) returns (RelaySecretCallResponse); // Runner-served, browser-UID-only broker socket; carries the value to the runtime
rpc RelaySecretCall(RelaySecretCallRequest) returns (RelaySecretCallResponse); // RunnerService
message BrokerFillRequest { FillSecretCall fill = 1; string origin = 2; } // origin from the runtime's own CDP state
message AgentSecretCallRequest {
  string call_id = 1;
  oneof call {
    RequestSecretRequest request = 2;
    GetSecretRequestCall get_request = 3;
    CancelSecretRequestCall cancel_request = 4;
  }
}
message SecretCallRequest { // Runner-to-Server relay form only.
  string call_id = 1;
  oneof call {
    RequestSecretRequest request = 2;
    GetSecretRequestCall get_request = 3;
    CancelSecretRequestCall cancel_request = 4;
    FillSecretCall fill = 5;
  }
}
message GetSecretRequestCall { string request_id = 1; }
message CancelSecretRequestCall { string request_id = 1; }
message FillSecretCall { string grant_id = 1; string use_id = 2; string field_locator = 3; }
message SecretCallResult {
  string call_id = 1;
  oneof result {
    SecretRequestCallResponse request = 2;
    GetSecretRequestCallResponse get_request = 3;
    CancelSecretRequestCallResponse cancel_request = 4;
    FillSecretCallResponse fill = 5;
    SecretCallError error = 6;
  }
}
message SecretCallError { string code = 1; string message = 2; } // e.g. permission_denied, grant_expired, origin_mismatch, revoked.
message SecretRequestCallResponse { string request_id = 1; }
message GetSecretRequestCallResponse { string state = 1; string grant_id = 2; int64 expires_at_unix_ms = 3; }
message CancelSecretRequestCallResponse { bool cancelled = 1; }
message FillSecretCallResponse { bool filled = 1; }
message RelaySecretCallRequest {
  string session_id = 1;
  SecretCallRequest call = 2;
  string origin = 3; // Browser-runtime CDP origin for fill only, never agent input.
}
message RelaySecretCallResponse {
  SecretCallResult result = 1;
  bytes secret_value = 2 [debug_redact = true]; // Runner forwards only to the session's browser runtime; never to the agent socket.
  int64 expires_at_unix_ms = 3;
}
```

`GetSecretRequest` and cancellation are restricted to the request's own resolved agent. Only the agent may cancel its pending request; a user denies it via `ReviewSecretRequest`. The Server derives caller identity from the live session binding.

### T3 — Runner relay and redemption

The Runner accepts `fill` only from the session's browser runtime on its browser-UID-only broker socket, and accepts request, status, and cancel only from the agent socket. It forwards the runtime-reported origin in `RelaySecretCallRequest.origin`, never in `FillSecretCall`. Denied, expired, and wrong-origin grants return `SecretCallError` codes through `SecretCallResult`; secret bytes travel only in the relay response field and go only to the browser runtime, never to the agent.

`Interfaces:`

```go
type RedemptionBinding struct { SessionID, Origin string }

type Redeemer interface {
    Redeem(context.Context, string, string, RedemptionBinding) (SecretValue, time.Time, error)
}

func (h *Hub) RelaySecretCall(ctx context.Context, runnerID string, req *compassv1internal.RelaySecretCallRequest) (*compassv1internal.RelaySecretCallResponse, error)
```

The Server normalizes the runtime-reported HTTPS origin, rejects opaque or non-HTTPS origins, and checks exact equality against the approved use. Request creation and approval apply the same origin validation; the approval panel shows ASCII/punycode hosts. The Server resolves `session_id = 1` through authenticated Runner state and requires a live durable binding for the requesting agent on every redemption. Only session grants additionally require the live binding version to equal the grant's version. It checks that USER ref scope id and approver still match the agent owner, or that the TENANT approver remains an admin. `Hub.releaseSession` and binding reaping revoke matching grants eagerly, but every redemption independently requires a live binding.

For once grants, hold the grant row lock through the backend read. On success, mark consumed and write the audit row in one transaction before returning bytes for the browser runtime; if commit fails, return no bytes. On backend read failure, audit and commit without consuming. Audit every redemption with ids, use kind, canonical binding, outcome, and time; never record the value.

### T4 — Approval panel and polling

Add a private Settings panel. Show requester, purpose, exact ref, proposed uses/origins as ASCII/punycode hosts, TTL, and grant mode; never show values. While open, poll `ListSecretRequests` and render the complete caller-authorized request snapshot, including state, grant id, and expiry. Do not publish an approval cue on the global `SubscribeEvents` bus.

### T5 — RIG-6007 browser consumer

Before enabling fill, RIG-6007 must keep the browser runtime and filled browser outside agent code-execution reach; keep the broker socket browser-UID-only; expose no agent-facing eval or filled-field DOM/value reads; and block or redact screenshots and accessibility-tree reads of filled fields. The runtime gets active origin from CDP and the Server matches it exactly to the approved origin. At fill time the runtime rechecks the target field's frame origin against the approved origin. It fills only an `input[type=password]`, or an input with `autocomplete=username`, `email`, or `one-time-code` when the same form contains a password input or a one-time-code input. It rejects all other fields. If navigation or frame identity/origin changes between redemption and fill, abort without writing the value. The broker cannot enforce browser isolation by itself.

## Tasks

- [ ] T1 — Add `0005_secret_broker.sql`, tenant-RLS broker tables, and encrypted Postgres `SecretBackend`. Test tenant isolation, AAD binding, encrypted storage, and independence from A9 resolution and materialization.
- [ ] T2 — Add broker-value set/delete, request creation, list, review, agent cancellation/status, and approver grant revocation APIs plus the single `Secret` call family and relay. Test auth-derived identity, AGENT-ref rejection, ownership, decision races, use-subset validation, expiry caps, authority loss, and revocation.
- [ ] T3 — Build the Runner relay (agent-socket and browser-UID-socket call split), origin-bound redemption, once-read locking, audit, live-agent-binding checks and session-only binding-version checks, and error-only agent results. Test `fill` rejected on the agent socket, foreign Runner, missing binding, stale session binding, wrong grant, origin mismatch, read-failure retry, redaction, and absence of model-visible values.
- [ ] T4 — Add the private Settings approval panel and polling of the caller-authorized request snapshot. Test that a USER owner sees only their own requests; a tenant admin sees only approvable requests in their tenant; and no caller sees another tenant's requests.
- [ ] T5 — Implement and test the RIG-6007 browser-isolation, credential-field, frame-origin, screenshot/accessibility-redaction, and navigation-race preconditions before enabling fill.

## Ledger delta

Add `docs/designs/compass/decisions/server/DL-639.md` for the core ruling: a Postgres control plane stores broker requests, grants, and audit; broker values use a separate encrypted Postgres table; and approved grants are redeemed only by the trusted browser runtime outside agent code. DL-350 stays Active and unchanged. DL-639 adds a separate broker-value class and does not change A9, the existing secret table, or boot/server-secret storage.
