# Compass Server on Kubernetes

> Freezes on merge; later changes supersede by citation, never rewrite.
> Unless a path says otherwise, every path is in
> [RigelBuild/compass](https://github.com/RigelBuild/compass) at `main`.

**Named decisions this record introduces:** D1–D9, inline in
[Design](#design), ledgered as DL-539 to DL-547 in `decisions/server/`.

## Open Questions

**OQ-12 (non-load-bearing) — Multi-arch Server image.** P1 publishes
`linux/amd64` only. `tools/server-image/build.ts` exits with "server-image
targets linux/amd64, but this host is ${process.arch}", and `flake.nix`
lists only `x86_64-linux`. The Server has no KVM need, so an arm64 node could
run it. The Runner stays amd64.

1. Publish a multi-arch index (`linux/amd64`, `linux/arm64`). This needs an
   `aarch64-linux` closure, an arm64 publish job and an index push, as
   `publishImageIndex` (`tools/agent-image-index/index.ts`) does for the
   agent image. `manifestIdentity` (`tools/runner-image/retag-core.ts`)
   rejects an index today ("an image index, or malformed"), so retag and the
   P6 tagger must learn to verify one.
2. Keep amd64 only. Operators place the Server with the P2 `nodeSelector`
   and `toleration` values.

**Recommendation:** 1. Nothing binds the Server to amd64, and an operator can
then run it on arm64 nodes next to one amd64 KVM node. P1–P6 hold under
either answer: P2 adds the optional placement values in both cases, because
it has none today. Option 1 lands as a follow-up to P1.

## Resolved decisions

Question numbers match the review threads on this record.

- **OQ-1 — Admin token from a Secret.** A Go change (P4). The supplied token
  file may be a symlink, and it may be group-readable when the process holds
  the file's group. There is no init container (D4).
- **OQ-2 — Server secret provider.** `dotenv://` from a read-only mounted
  Secret file (D4).
- **OQ-3 — LLM gateway.** Not deployed by this record (D1).
- **OQ-5 — Postgres.** The operator brings a Postgres that meets the D3
  contract.
- **OQ-6 — Image lane reuse.** `tools/server-image/` reuses the pure cores of
  `tools/runner-image/` behind `--lane runner|server` (P1).
- **OQ-7 — Image visibility.** Every Compass image is public (D9, P5).
- **OQ-8 — Sessions across a Server restart.** The Runner must survive a
  Server restart. That is its own record. Until it lands, see
  [Known limitation](#known-limitation).
- **OQ-9 — Who runs Postgres.** Never the renderer (D1, D3).
- **OQ-10 — Digest bumps.** Paired per-commit tags that image automation
  follows (D8, P6).

## Problem / Intent

The Runner runs on Kubernetes: it has a digest-published image, a manifest
renderer, and projected-token identity. Nothing deploys a Compass Server
there, so the Runner's `serverAddr` has no target. This record lists the
product changes that let an operator run Server plus Runner on a cluster, and
the requirements the product places on that operator. How one operator runs
its own clusters is out of scope; see
[self-host and managed](https://github.com/RigelBuild/compass/blob/main/docs/concepts/self-host-and-managed.md)
and the [OSS-core boundary record](meta/oss-core-managed-boundary/design.md).

## Approach

### Ground truth

- **Stack children.** `spawnChain` (`go/internal/stack/stack.go`) starts
  Postgres, the OTel collector, NATS, the LLM gateway, compass-server, then
  compass-runner. The first four are podman containers. The Server gets the
  door as `--listen-fd 3` (`serverSpec`, `go/internal/stack/spec.go`).
- **Boot order.** `Serve` (`go/server/serve.go`) binds listeners, then
  `openFabricAndStore` opens NATS before the store. `Open`
  (`go/internal/store/store.go`) pings once without retry ("store: ping"),
  then applies the embedded migrations under the session-scoped advisory
  lock `migrationLockKey`.
- **RLS roles.** `0001_init.sql` runs `CREATE ROLE compass_system NOLOGIN
  BYPASSRLS`, always re-runs `ALTER ROLE compass_system BYPASSRLS`, and grants
  `compass_app` and `compass_system` to `current_user`. PostgreSQL lets only
  a superuser or a role holding BYPASSRLS set that attribute
  ([CREATE ROLE](https://www.postgresql.org/docs/current/sql-createrole.html)).
  `WithSystemRole` (`go/internal/store/tenant_tx.go`) runs `SET LOCAL ROLE
  compass_system` at runtime.
- **Door.** `loadNetworkTLS` (`go/server/network_door.go`) loads the keypair
  once, with a TLS 1.3 floor and h2. It has no reload.
- **Health.** `go/server/health.go` serves `GET /healthz` and grpc health. It
  reports NOT_SERVING once drain starts. `compass.runner` is SERVING only
  while a Runner is enrolled.
- **Secrets.** `resolveMasterKey` (`go/server/serve.go`) fails boot without a
  64-hex `COMPASS_MASTER_KEY`, and `reconcileKeyState` fails boot when it
  differs from the stored key. The resolver in
  `go/internal/secrets/resolver.go` names the dotenv file when it cannot open
  it ("secrets: dotenv provider file %s: %w"), and secretspec writes an audit
  log under `$XDG_STATE_HOME`.
- **Admin token.** `readSuppliedAdminToken` (`go/server/network_door.go`)
  `Lstat`s the path and fails on `info.Mode()&(os.ModeType|0o077) != 0`. A
  Secret volume file is a symlink through `..data`. With `fsGroup`, kubelet's
  `changeFilePermission` (`pkg/volume/volume_linux.go` in
  kubernetes/kubernetes) runs `os.Lchown(filename, -1, int(*fsGroup))` and
  adds `roMask = os.FileMode(0440)` on a read-only volume. The file fails
  both checks today.
- **Runner.** `renderRunnerManifests` (`tools/runner-manifests/render-core.ts`)
  takes an absolute `https` `serverAddr`, and now `serverCA` and
  `egressAllow`. The hub holds one Runner (`go/internal/runnerhub/hub.go`).
- **Agent egress.** The microVM arms a default-deny firewall.
  `--egress-allow` (`go/cmd/compass-runner/main.go`) is the only way to open
  a host. `isValidHost` (`go/internal/runtime/host.go`) admits DNS names and
  IP literals, never `host:port`.
- **Issuer keys.** `fetchHTTPS` (`go/internal/auth/workload_identity.go`)
  sends no `Authorization` header. A `jwksFile` entry never fetches.
  `validateRunnerClusters` (`go/internal/auth/runner_clusters.go`) requires
  issuers to be unique only among the configured entries.
- **Image lanes.** Each lane publishes `:git-<sha12>` by digest only when its
  closure set changed. `resolveAncestor` (`tools/runner-image/retag-core.ts`)
  walks first-parent ancestors and returns the newest published one;
  `assertNoClosureChange` fails when a skipped commit changed the closure.
  `release-runner-image` and `release-server-image`
  (`.github/workflows/release.yml`) use them to mint `:vX.Y.Z` by digest.
- **Tag order.** No published tag sorts by commit. Commit timestamps are not
  unique: 29 of the last 100 main commits share their second with another
  commit. Flux's `RegexFilter.Apply` (`internal/policy/filter.go` in
  fluxcd/image-reflector-controller) stores `f.filtered[tag] = item`, so two
  tags with the same extracted value collapse to one.
- **Node shape** ([spike findings](platform/compass-runner-containerization/spike-findings.md)).
  `TestFullBoot` passed on a bare-metal node with `/dev/kvm` `0666`, a
  device plugin and `hostUsers: false`. A host with AppArmor needed
  `kernel.apparmor_restrict_unprivileged_userns=0`.

### Design

**Topology (D1).** Each stack child that runs as a container today becomes
its own pod. The Server container supervises nothing.

| Child | On Kubernetes |
| --- | --- |
| compass-server | Deployment, `replicas: 1`, `strategy: Recreate` |
| NATS | StatefulSet, 1 replica, PVC, `DefaultNatsImage`, the stack's R1 file JetStream config |
| Postgres | operator-provided, outside the renderer (D3) |
| OTel collector | operator-provided; `OTEL_EXPORTER_OTLP_ENDPOINT` points at it |
| LLM gateway | not deployed; see the [gateway record](server/compass-server-llm-gateway/design.md) |
| compass-runner | the existing DaemonSet, on exactly one KVM node |

One Server replica, because the hub holds one Runner and the
[delivery-cutover record](infra/runtime/compass-managed-delivery-cutover/design.md)
keeps a single Server. `Recreate` also keeps two Servers from migrating at
once. Exactly one Runner pod per Server: `enroll` (`go/internal/runnerhub/hub.go`)
replaces the attached Runner and drives its bound accounts offline, so a
second Runner drops the first one's sessions. Only `compass-stack` needs a
gateway ("gateway image is required; pass --gateway-image or
--gateway-external", `go/internal/stack/stack.go`); `compass-server` has no
gateway flag. Agents keep a raw provider key plus an egress allowlist (D7).

**Server image (D2).** `ghcr.io/rigelbuild/compass-server` follows the Runner
image's shape. `server-image/Dockerfile` copies the realised
`.#compass-server` closure onto a digest-pinned distroless `static-debian12`
base. The image runs as `65532:65532`, with `/nix/store/.compass-server` as
its entrypoint. The closure carries secretspec and the wrapper's bash.
`SERVER_IMAGE_CLOSURE_PATHS` gates publishing.

**Data and migrations (D3).** Migrations stay in Server boot; there is no
Job. The renderer never renders Postgres. The operator's Postgres must meet
this contract:

- The DSN names a direct, session-mode endpoint. The migration lock is
  session-scoped, so a transaction-mode pooler breaks it.
- The login role owns the target database, so it can create objects in
  `public`.
- The login role holds CREATEROLE plus BYPASSRLS, or is superuser, because
  `0001_init.sql` creates and re-asserts the BYPASSRLS role. A service whose
  owner role cannot hold BYPASSRLS cannot run today's schema.
- The database answers at boot. A suspended database fails the single ping,
  and the pod restarts until it answers.
- One replica fits the default pool of 10.

**Secrets (D4).** The renderer names Secrets but never holds a value. The
operator creates every Secret and backs up the master key; losing it is
unrecoverable (`docs/self-host.md`).

- `COMPASS_DATABASE_DSN`, `COMPASS_NATS_URL` and `COMPASS_S3_*` come from
  Secrets as env.
- Server secrets come from one Secret key, `server.env`, mounted read-only
  and read through `--secret-provider dotenv:///etc/compass/secrets/server.env`.
  The file holds `COMPASS_MASTER_KEY` and the `SERVER_<name>` values.
  `SpecResolver.buildManifest` (`go/internal/secrets/resolver.go`) declares
  exactly those names. Unlike `env://`, the file keeps the master key out of
  the process env.
- `SERVER_*` values re-resolve on use. `COMPASS_MASTER_KEY` is read once at
  boot and must never change in the mounted Secret.
- `XDG_STATE_HOME` and `--state-dir` sit on one emptyDir under `/run/compass`.
  The state dir is a child the Server creates at `0700` (`ensurePrivateDir`,
  `go/server/socket.go`).
- The admin token Secret mounts directly, `defaultMode: 0440`, under pod
  `fsGroup: 65532`. `--admin-token-file` names the projected file. P4 makes
  the Server accept it. No init container and no init image.
- Agents get forge tokens only from the Server's App broker.
  `buildGitCredentialBroker` (`go/server/git_credential.go`) returns nil
  unless the primary App id is set, and `IsReservedGitHubEnvName`
  (`go/internal/secrets/secrets.go`) strips user `GH_TOKEN`-style secrets. So
  the renderer takes a `forge` value and renders `--forge-*` args: ids and
  secret names, never values. The reviewer App is optional;
  `wireForgeWriteCaller` (`go/server/serve.go`) wires agent `ForgeCall`
  writes only when both Apps are set.
- An admin stores each agent provider key with `compass secret set <NAME>
  --kind provider --provider <id> --delivery file`.

**NATS access (D5).** The stack's NATS has no auth because it listens on
loopback. In a cluster, NATS requires a user and password, and a
NetworkPolicy admits port 4222 only from the Server pod. The password reaches
the NATS config through a variable reference, and the credentials ride in
`COMPASS_NATS_URL`. The fabric's connect path keeps URL credentials out of its
errors (`go/internal/fabric/fabric.go`).

**Door, TLS and serverAddr (D6).** The Server terminates its own TLS. No
ingress may terminate it: the door refuses cleartext, and the Runner's bidi
stream needs h2 end to end.

- **Certificate.** The operator supplies a `kubernetes.io/tls` Secret whose
  SANs cover the in-cluster name and any external name. A private CA works;
  the Runner's `serverCA` value and the client `--ca` carry it.
- **Renewal.** The door loads its keypair once, so a renewed certificate
  takes effect on restart (see [Known limitation](#known-limitation)).
- **Services.** Service `compass-server` (ClusterIP) maps 443 to 50052. An
  optional LoadBalancer Service on 50052 passes TCP through.
- **serverAddr** is `https://compass-server.<namespace>.svc`.
- **Clients.** The CLI uses `--server-addr`, `--ca` and the admin token
  (`go/cmd/compass/client.go`). The desktop app uses client mode with
  `ca_cert` (`docs/self-host.md`). A browser origin comes from
  `corsAllowedOrigin`.
- **Probes.** Startup and readiness are HTTPS `httpGet` on `/healthz`.
  Readiness never depends on `compass.runner`, because the Runner needs the
  Service before it can enroll. There is no liveness probe.

**Runner values (D7).**

- **Identity.** A cluster whose issuer serves no anonymous discovery uses a
  `jwksFile` entry: the operator reads `/openid/v1/jwks` at render time and
  passes it as a `jwks` value, and the Server renderer writes it into the
  runner-clusters ConfigMap. This needs no Go change. A signing-key rotation
  then needs a re-render and a Server rollout. An issuer with anonymous
  discovery keeps `jwksURI`.
- **CA.** `serverCA` mounts the CA and sets `COMPASS_RUNNER_CA`.
- **Egress.** `egressAllow` renders one `--egress-allow=<hosts>` arg. The
  renderer applies the `isValidHost` grammar, so `host:port` fails at render
  time. A session needs the provider API host, `github.com` and
  `api.github.com`.
- **Node.** The KVM node exposes `/dev/kvm` as the extended resource named by
  `kvmResourceName`, stages the seccomp profile at `seccompProfilePath`
  under the kubelet seccomp root, and supports `hostUsers: false`.

**Delivery split and paired tags (D8).** Public compass holds
`tools/server-manifests/` and `tools/runner-manifests/`: values in, plain
objects out, digest-only images, no Secrets. The operator holds the values
for each cluster, the Secrets, the certificate, node config, and how the
output is applied. Every main commit gets `:main-<n>-<sha12>` on both the
Server and Runner images (P6). `n` is the commit's first-parent count on
main. Each lane's tag names that lane's newest publish at or before the
commit, re-tagged by digest with no rebuild. Both tags of one `n` therefore
pair the images built from the same Go inputs, as long as both closure sets
cover all of `go/**` (compass PR #2357). Main requires linear history and
forbids force-pushes, so `n` rises by one per main commit and is unique. Image
automation follows the tags with a numerical policy on `n`:

```yaml
filterTags:
  pattern: '^main-(?P<n>[1-9][0-9]*)-[0-9a-f]{12}$'
  extract: '$n'
policy:
  numerical:
    order: asc
digestReflectionPolicy: IfNotPresent
```

One automation over both policies bumps both images in one commit. No
operator bump job is needed. The tagger writes the Server tag, then the
Runner tag, seconds apart. A scan between the two writes sees a new Server
with the previous Runner for one interval; the next scan pairs them.

**Public images (D9).** Every Compass image is public:
`compass-agent`, `compass-runner`, `compass-server`, `compass-guest-image`
and `compass-gateway` under `ghcr.io/rigelbuild`. On 2026-10-17 all five
answered `skopeo list-tags --no-creds`. Neither renderer emits
`imagePullSecrets`. Tooling must not assume registry auth for reads. GitHub's
REST packages API can list, get, delete and restore a package, but has no
call that sets visibility. A new package starts private, and making it public
is a one-way switch in the package settings: a manual step at its first
publish ([package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)).

### Known limitation

A Server restart ends every live session. A dropped session stream ends
`Run` (`go/internal/runner/run.go`, "runner sessions loop: %w"), and the
Runner exits. The Runner has no liveness probe because "a pid-1 restart tears
down all node sessions" (`tools/runner-manifests/render-core.ts`). Each
Server deploy and each certificate renewal therefore drops sessions, and D8
makes deploys frequent. A separate record makes the Runner survive a Server
restart: it re-dials, re-enrolls with the hub, and keeps its microVMs.

### Alternatives considered

- **One pod running `compass-stack up`.** Rejected: the supervisor drives
  podman and host processes.
- **An init container that copies the admin token.** Rejected: it adds an
  image value and a pod step for a check the Server can make itself (P4).
- **A renderer-owned Postgres StatefulSet.** Rejected: no upgrade path and no
  point-in-time recovery, and it would read as a supported surface.
- **Ingress-terminated TLS.** Rejected: an extra h2 hop and a second
  certificate.
- **A migration Job.** Rejected: no migrate-only entrypoint exists, and the
  advisory lock serializes migrations.
- **An operator bump job, or per-image automation on `:git-<sha12>`.**
  Rejected: the first is a scheduled job per operator; the second has no
  order and pairs nothing.
- **A timestamp tag.** Rejected: equal timestamps collapse in the policy.

## Plan

### Global Constraints

- **Public repo.** No operator names, hosts, zones, cluster names or private
  product detail.
- **PRs.** Each task is one compass PR, red then green. P2 merges after P4,
  because its output boots only with P4. P6 stacks on P5: both edit
  `tools/runner-image/` tests and `release.yml`.
- **Images.** Digest only. Renderers reject tags, as `assertRunnerImageDigest`
  does.
- **Secrets.** Renderers emit no Secret object and no secret value.
- **Kubernetes.** ≥1.33, for user namespaces.
- **Server pod.** uid and gid 65532, `fsGroup: 65532`,
  `readOnlyRootFilesystem`, drop ALL, `RuntimeDefault` seccomp,
  `automountServiceAccountToken: false`.
- **Lanes.** TypeScript is Bun with `bun:test`. Go DB tests use the `pgtest`
  build tag.

### Order

```text
P1 server image (#2355, merged) ─┐
P3 runner values (#2360, merged) ┼──► operators render and deploy
P4 admin token (Go) ──► P2 server renderer (#2363) ┘
P5 public images ──► P6 paired main tags
Deferred: F1 door TLS reload, F2 tokenFile
```

### P1 — Server image and publish lane

Merged as compass PR #2355, after #2357 widened `RUNNER_IMAGE_CLOSURE_PATHS`
to `go/**`. Its smoke checks `--version`, a missing-DSN exit 1, user
`65532:65532`, a read-only `dotenv://` boot with Postgres and NATS, and a
reproducible digest.

```text
server-image/{Dockerfile,moon.yml,.dockerignore}
tools/server-image/{build.ts,build-core.ts}   // ENTRYPOINT_LINK = ".compass-server", IMAGE_USER = "65532:65532"
tools/runner-image/image-lane-core.ts          // imageLaneConfig(lane: string | undefined): ImageLaneConfig | undefined
tools/runner-image/{publish.ts,retag.ts}       // --lane runner|server
.github/workflows/release.yml                  // publish-server-image, release-server-image
SERVER_IMAGE_CLOSURE_PATHS: server-image/** tools/server-image/** tools/runner-image/** go/**
  tools/toolchain/secretspec-env.nix devenv.lock flake.nix flake.lock version.txt
  .github/workflows/release.yml
```

### P2 — Server manifests renderer

Open as compass PR #2363. Against its current head it must drop
`initImage`, the `init-admin-token` init container and the `postgres` value
with its StatefulSet, Service and NetworkPolicy. It must add `forge`,
`nodeSelector` and `toleration`. Tests use golden output plus invariants:

- `replicas: 1` with `Recreate`; digest-only images.
- No `Secret` kind, no `initContainers`, no Postgres object.
- `parseServerDeployValues` rejects a `postgres` or `initImage` key with an
  error naming the key, so an older values file fails instead of rendering
  without them.
- The admin token volume has `defaultMode: 0o440`, and `--admin-token-file`
  names the file in that volume.
- The NATS NetworkPolicy admits 4222 only from the Server label.
- Probes hit `/healthz`; no liveness probe.
- `XDG_STATE_HOME` and `--state-dir` sit on the emptyDir, and the state dir
  is not its root.
- A `runnerClusters` entry with `jwks` renders a ConfigMap key and a matching
  `jwksFile`; `jwks` plus `jwksURI` is rejected.
- `forge` renders `--forge-host`, `--forge-app-id`, `--forge-installation-id`,
  `--forge-app-key-secret`, `--forge-app-webhook-secret`, and `--forge-repos`
  when set. Ids are positive integers. `forge.reviewer` adds the three
  `--forge-reviewer-*` args. An absent value renders no arg.
- `nodeSelector` and `toleration` render as in the Runner renderer, with
  `operator: "Equal"`; absent values render none.

The README covers the render steps, the JWKS read, the D3 Postgres contract,
the `server.env` lines, and the never-change rule for `COMPASS_MASTER_KEY`.

```ts
// tools/server-manifests/render-core.ts
export type RunnerClusterValues = {
  name: string; issuer: string; audience?: string;
  namespace: string; serviceAccount: string; maxTokenLifetime?: string;
  jwksURI?: string; jwks?: string; caFile?: string;
};
export type ForgeValues = {
  host?: string; repos?: string[];
  appId: number; installationId: number;
  appKeySecret: string; appWebhookSecret: string;
  reviewer?: { appId: number; installationId: number; appKeySecret: string };
};
export type ServerDeployValues = {
  namespace: string; serverImage: string; natsImage: string; natsStorage: string;
  tlsSecretName: string; dsnSecretName: string; natsSecretName: string;
  serverSecretsSecretName: string; adminTokenSecretName: string; s3SecretName?: string;
  corsAllowedOrigin?: string; otlpEndpoint?: string;
  external?: { port: number; loadBalancerClass?: string };
  nodeSelector?: { key: string; value: string };
  toleration?: { key: string; value: string; effect: "NoSchedule" | "PreferNoSchedule" | "NoExecute" };
  forge?: ForgeValues;
  runnerClusters: RunnerClusterValues[];
};
export function assertServerImageDigest(ref: string): void;
export function parseServerDeployValues(raw: unknown): ServerDeployValues;
export function renderServerManifests(v: ServerDeployValues): ReturnType<typeof renderRunnerManifests>;
// server args: --listen 0.0.0.0:50052 --tls-cert /etc/compass/tls/tls.crt
//   --tls-key /etc/compass/tls/tls.key --state-dir /run/compass/state
//   --socket /run/compass/state/server.sock
//   --admin-token-file /var/run/compass/admin-token/token
//   --runner-clusters /etc/compass/runner-clusters/clusters.yaml
//   --secret-provider dotenv:///etc/compass/secrets/server.env [--forge-*]
// files: tools/server-manifests/{render.ts,values.example.json,README.md,moon.yml}
```

### P3 — Runner renderer `serverCA` and `egressAllow`

Merged as compass PRs #2358 and #2360. `serverCA` takes exactly one of
`configMapName` or `secretName`, plus `key`, mounts at
`/etc/compass/server-ca` and sets `COMPASS_RUNNER_CA`. `egressAllow` renders
one `--egress-allow=` arg and rejects hosts outside `isValidHost`.

```ts
// tools/runner-manifests/render-core.ts
RunnerDeployValues.egressAllow?: readonly string[];
RunnerDeployValues.serverCA?:
  | { configMapName: string; secretName?: undefined; key: string }
  | { secretName: string; configMapName?: undefined; key: string };
```

### P4 — Admin token file from a Secret volume (Go)

`readSuppliedAdminToken` opens the path once, which follows the symlink, and
checks the opened file's `Stat`. That removes the gap between check and read.
The file must be regular, with no setuid, setgid or sticky bit, no
group-write bit and no other bit. Group-read is allowed only when the file's
gid is the process's `os.Getegid()` or in `os.Getgroups()`. kubelet's 0440
file under `fsGroup` passes; a 0640 file in a group the process does not
hold fails. `writeSuppliedAdminTokenFile` still copies the token to a 0600
file under the state dir, and `reusableAdminToken` keeps its strict `Lstat`
check for that copy. Both are unchanged.

Tests, red first:

- `go/server/admin_token_mode_test.go` (`//go:build unix`), on the pure
  check: 0600 passes; 0640 and 0440 pass with a held gid; 0640 fails with an
  unheld gid; 0660, 0604, a directory and a setgid file fail.
- `TestIssueAndWriteAdminTokenRejectsMalformedSuppliedFiles`
  (`go/server/admin_token_reuse_pgtest_test.go`) drops its "group readable"
  and "symlink input" rejection cases and adds "group writable" (0660) and
  "other readable" (0604).
- A new pgtest builds the Secret volume layout: `..data/token` at 0440 in
  `os.Getegid()`, and `token -> ..data/token`. Boot registers the token,
  `assertAdminTokenAuthenticates` passes, and the state-dir copy is 0600.

The spec clause in `docs/specs/product/compass.md` ("SHALL name a private
regular file") and the `--admin-token-file` help in
`go/cmd/compass-server/main.go` ("Private file containing the supplied
bootstrap-admin token") change to the new rule.

```go
// go/server/network_door.go (//go:build unix)
func readSuppliedAdminToken(path string) (string, error) // os.Open, f.Stat, io.ReadAll(f)
// checkSuppliedTokenMode is pure; gid comes from info.Sys().(*syscall.Stat_t).Gid.
func checkSuppliedTokenMode(mode os.FileMode, gid uint32, held []int) error
```

### P5 — Public images and anonymous reads

The five packages are public already. P5 makes the tooling match and keeps
it true.

- `retag.ts` probes with `skopeo inspect --no-creds --raw`; only the copy
  uses `REGISTRY_AUTH_FILE`. `classifyProbe` still reads 403 as ambiguous,
  because a private package and a missing package give the same 403.
- Stale text goes: the `release-runner-image` step comment "The runner
  package is private, so even the probes need auth", the `retag.ts` header
  "so private packages work", and the `retag-core.test.ts` case name "a
  private package without auth (403 Forbidden) is ambiguous" and its "on
  this private package" comment.
- A new check runs as the last step of each of the five publish jobs. It
  reads the just-published tag anonymously and fails with the package
  settings path when the package is not public. A new package therefore
  fails its first publish until someone sets it public: the one manual step,
  because no API sets visibility.

Tests: `assert-public-core.test.ts` maps a manifest to public, and a 403, a
"manifest unknown" and a timeout to failures that name the package.
`retag-core.test.ts` asserts the probe argv carries `--no-creds` and the copy
argv carries `--authfile`.

```ts
// tools/runner-image/assert-public-core.ts
export function publicVerdict(result: ProbeResult, ref: string): { ok: true } | { ok: false; message: string };
// tools/runner-image/assert-public.ts  bun tools/runner-image/assert-public.ts --ref <repo:tag>
// tools/runner-image/retag-core.ts
export function probeArgv(repo: string, sha12: string): string[]; // ["inspect","--no-creds","--raw","docker://<repo>:git-<sha12>"]
```

### P6 — Paired main tags

A new job `tag-main-images` in `.github/workflows/release.yml` runs on every
main push. It `needs: [publish-runner-image, publish-server-image]`, so it
runs after both lanes publish or skip, and never when either failed. It has
`permissions: {contents: read, packages: write}`, its own concurrency group
with `cancel-in-progress: false` and `queue: max`, and a checkout with
`fetch-depth: 0`; a shallow clone gives a wrong `n`.

`tag-main.ts` computes `n` with `git rev-list --count --first-parent <sha>`.
For each lane it runs `resolveAncestor` over `git rev-list --first-parent
--max-count=50 <sha>` and `assertNoClosureChange` over the diff from the
resolved commit, as `retag.ts` does. It resolves both lanes before any write.
Then it copies each digest to `:main-<n>-<sha12>` with `--preserve-digests`,
Server first, and checks each with `assertCoherent`. A lane that published
this commit resolves at position 1.

Tests, red first:

- `retag-core.test.ts`: `mainTag(1638, <40-hex>)` returns
  `main-1638-<sha12>`; 0, a fraction and a short sha throw `EXIT.usage`.
- `resolveMainPair`: both lanes found at the head; a lane absent at the head
  with no closure change resolves its ancestor; a lane absent with a closure
  change throws, and the injected writer records no call.
- `tools/runner-image/closure-paths.test.ts`: `go/gen/compass/v1/compass.pb.go`
  hits both closure sets through `closureChanges`; `tag-main-images` needs
  both publish jobs and checks out with `fetch-depth: 0`.
- The tag matches the D8 pattern, and Flux's extract yields `n`.

```ts
// tools/runner-image/retag-core.ts
export function mainTag(firstParentCount: number, commit: string): string; // "main-<n>-<sha12>"
export async function resolveMainPair(input: {
  ancestors: readonly string[];
  probe: Record<ImageLane, (sha12: string) => Promise<ProbeResult>>;
  changedSince: (commit: string) => Promise<string[]>;
  closurePaths: Record<ImageLane, string>;
}): Promise<Record<ImageLane, Resolved>>;
// tools/runner-image/tag-main.ts
//   bun tools/runner-image/tag-main.ts --sha <40-hex> \
//     --server-repo ghcr.io/rigelbuild/compass-server --runner-repo ghcr.io/rigelbuild/compass-runner
// .github/workflows/release.yml: job tag-main-images
```

### Deferred follow-ups

- **F1 — Door reloads its TLS keypair (Go).** Taken if renewals must not
  restart the Server. Tests: after a rewrite the next handshake presents the
  new serial; a corrupt rewrite keeps the old one.

```go
// go/server/network_door.go
func newCertReloader(certFile, keyFile string, log *slog.Logger) (*certReloader, error)
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
```

- **F2 — `tokenFile` on a Runner cluster (Go).** Taken when a cluster needs
  key rotation without a rollout. Key fetches send `Authorization: Bearer`
  with the file's contents, re-read on every fetch.

```go
// go/internal/auth/runner_clusters.go
RunnerCluster.TokenFile string // yaml:"tokenFile", optional
// go/internal/auth/workload_identity.go
func fetchHTTPS(ctx context.Context, client *http.Client, u, bearer string) ([]byte, error)
```

## Tasks

- [x] P1 — Server image and publish lane, compass PR #2355, after #2357
- [ ] P2 — `tools/server-manifests` renderer: no Postgres, no init container,
  `forge`, placement values (compass PR #2363), after P4
- [x] P3 — Runner renderer `serverCA` and `egressAllow`, compass PRs #2358
  and #2360
- [ ] P4 — Admin token file from a Secret volume (Go)
- [ ] P5 — Public images; tooling reads anonymously and checks visibility
- [ ] P6 — Paired `:main-<n>-<sha12>` tags on every main commit, after P5
