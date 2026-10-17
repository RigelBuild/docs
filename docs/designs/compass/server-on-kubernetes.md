# Compass Server on Kubernetes — self-hosted k3s first, then EKS

> Freezes on merge; later changes supersede by citation, never rewrite.
> Unless a path says otherwise, every path is in
> [RigelBuild/compass](https://github.com/RigelBuild/compass) at `main`.
> Compass design records moved out of `docs/designs/`; this record cites them
> at commit [`53ab753`](https://github.com/RigelBuild/compass/tree/53ab753f089ef1293d66736f247d41d38063dc7f/docs/designs),
> where they last lived.

**Named decisions this record introduces:** D1–D9, inline in
[Design](#design), ledgered as DL-539 to DL-547 in `decisions/server/`.

## Open Questions

Each question names a recommendation, and the design is written against it.
OQ-2 and OQ-3 are closed by the source and stay as rationale.

**OQ-1 (non-load-bearing) — Bootstrap admin token from a Secret.**
`readSuppliedAdminToken` (`go/server/network_door.go`) rejects a symlink and
any group or other bit. A Secret volume file is a symlink, and with `fsGroup`
kubelet adds group bits, so a Go relaxation does not help either.

1. An init container copies the token into a `0600` file on an emptyDir.
   The init image is `busybox`, pinned by digest in the values, because the
   distroless Server image has no `cp` and its wrapper's bash has no stable
   path.
2. A Go change accepts a symlinked file with a `0400` target.
3. No supplied token: the Server mints one into its state dir on every
   restart.

**Recommendation:** 1. It needs no Go change, and it keeps the token stable
across restarts.

**OQ-2 (closed) — Server secret provider.** `dotenv://` from a read-only
mounted Secret file. `SpecResolver.buildManifest`
(`go/internal/secrets/resolver.go`) declares the exact stored names
(`COMPASS_MASTER_KEY`, `SERVER_<name>`). The flake check
`tools/flake-gate/compass-server-secretspec.nix` and the e2e fixture
(`go/e2e/fixture.go`) already boot through `dotenv://`. Unlike `env://`, the
file keeps the master key out of the Server's process env, and secretspec
re-reads it on each resolve, so a rotated Secret needs no restart.

**OQ-3 (closed) — LLM gateway placement.** Phase one deploys no gateway.
Only `compass-stack` refuses to start without one (`go/internal/stack/stack.go`:
"gateway image is required; pass --gateway-image or --gateway-external"), and
D1 drops compass-stack on Kubernetes. `compass-server` has no gateway flag,
and no code under `go/` delivers a gateway URL to agents yet: that is T5 of
the [gateway record](https://github.com/RigelBuild/compass/blob/53ab753f089ef1293d66736f247d41d38063dc7f/docs/designs/server/compass-server-llm-gateway/design.md).
Agents keep today's shape, a raw provider key plus an egress allowlist (D7).
The gateway's Kubernetes placement, and its service token, belong to a later
record.

**OQ-4 (non-load-bearing) — Server certificate.** DNS-01 needs only a public
zone, not a reachable host, so both options work on a self-hosted k3s cluster.

1. A long-lived certificate (for example one year) from the cluster's
   existing private-CA ClusterIssuer. The Runner needs a `serverCA` value
   (T5), and clients need `--ca` or `ca_cert`. `serverAddr` stays
   `https://compass-server.<namespace>.svc`.
2. A public DNS-01 certificate for the external name. This drops the CA
   plumbing. The Runner then dials the external name, which must resolve
   in-cluster (split-horizon DNS or a CoreDNS rewrite), and the `.svc` SAN is
   lost.

**Recommendation:** 1. It reuses an issuer that exists, and it keeps the
Runner on the in-cluster name. EKS may choose either.

**OQ-5 (load-bearing for EKS only) — Managed Postgres and BYPASSRLS.**
`0001_init.sql` runs `CREATE ROLE compass_system NOLOGIN BYPASSRLS` under
`IF NOT EXISTS`, then always runs `ALTER ROLE compass_system BYPASSRLS`.
PostgreSQL lets only a superuser or a role that holds BYPASSRLS set that
attribute ([CREATE ROLE](https://www.postgresql.org/docs/current/sql-createrole.html)).
`WithSystemRole` (`go/internal/store/tenant_tx.go`) uses the role at runtime,
so it is load-bearing. The RDS master user is `NOSUPERUSER`
([rds_superuser](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Appendix.PostgreSQL.CommonDBATasks.Roles.rds_superuser.html)),
so roles created out of band do not help there.

1. A managed provider whose owner role holds BYPASSRLS and CREATEROLE. Neon
   grants BYPASSRLS to its owner role for projects created after
   2023-08-15.
2. An in-cluster Postgres operator on EKS, whose superuser runs the
   migration unchanged.
3. RDS or Aurora, with a schema change that drops the BYPASSRLS role. This
   is a Go and SQL record of its own.

**Recommendation:** 1. It needs no code change. T7 confirms the owner role's
attributes before it wires the DSN.

**OQ-6 (non-load-bearing) — Image lane reuse.**

1. A new `tools/server-image/` imports the pure cores of
   `tools/runner-image/`.
2. Generalize `tools/runner-image/` to build both images.

**Recommendation:** 1. The Runner build carries a kernel and guest image that
the Server does not need. T3 (compass PR #2355) implements 1 and adds a
`--lane runner|server` flag to the shared publish and retag scripts.

**OQ-7 (non-load-bearing) — Server image visibility.** The Runner package is
private (`tools/runner-image/retag.ts`).

1. The Server package is public.
2. The Server package is private, and clusters need a pull secret.

**Recommendation:** 1. The Server binary already ships in public release
assets.

**OQ-8 (non-load-bearing) — Sessions across a Server restart.** A dropped
session stream ends `runner.Run` with "runner sessions loop" and the Runner
exits (`go/internal/runner/run.go`). The renderer has no liveness probe
because "a pid-1 restart tears down all node sessions"
(`tools/runner-manifests/render-core.ts`). Every Server deploy and every
certificate renewal therefore kills live sessions.

1. Accept the loss in phase one. Deploys and renewals are rare and
   operator-timed.
2. A separate record makes the Runner survive a Server restart: it re-dials,
   re-enrolls with the hub, and keeps its microVMs.
3. Add in-process TLS reload to the door. This covers renewals only, not
   deploys.

**Recommendation:** 1 now, and file 2 as its own record. Option 3 is the
deferred follow-up F1 in [Plan](#plan).

**OQ-9 (non-load-bearing) — Who runs Postgres on k3s.**

1. The Server renderer keeps an optional Postgres StatefulSet, marked
   dev-only, with a backup note.
2. The renderer never renders Postgres. The operator installs a Postgres
   operator on k3s, and EKS uses a managed database.

**Recommendation:** 1. It is the fastest path, and the operator's Postgres
operator is not installed on the k3s cluster today. An absent `postgres` value
already renders no Postgres objects, so moving to 2 later needs no renderer
change. The StatefulSet is not a supported product surface: it has no major
upgrade path and no point-in-time recovery.

**OQ-10 (non-load-bearing) — Digest bumps.** A dev cluster tracks main, but
D8 pins both digests to one compass commit.

1. Re-render by hand on each bump.
2. A scheduled job in the operator's infra repo resolves both digests at one
   compass commit, re-renders, and opens a pull request.
3. Flux image automation. It bumps each image alone, which breaks the
   one-commit pairing.

**Recommendation:** 2, sized as a task in T6. Each merged bump restarts the
Server (OQ-8).

**OQ-11 (non-load-bearing) — Dev state.** Moving the current dev database
needs a dump and restore and the same master key. `reconcileKeyState`
(`go/server/serve.go`) fails boot on a key mismatch. Today's dev Server reads
the key through secretspec's default chain.

1. Fresh install. Operators re-enter secrets with `compass secret set` and
   push agent configs again.
2. `pg_dump` the dev database, restore it, and seed the same master key.

**Recommendation:** 1. Dev holds no history worth the key-handling risk, and
the restore path is untested.

## Problem / Intent

The Runner can run on Kubernetes: it has a digest-published image, a manifest
renderer, and projected-token identity. Nothing deploys a Compass Server
there, so the Runner's `serverAddr` has no target. This record adds a Server
image, a public renderer for the Server and its data services, the Runner
values that agents need, and the node and delivery steps. With them, Server
plus Runner run end to end on a self-hosted k3s cluster, and the dev environment moves
there. EKS follows.

## Approach

### Ground truth

- **Stack children.** `spawnChain` (`go/internal/stack/stack.go`) starts
  Postgres, the OTel collector, NATS, the LLM gateway, compass-server, then
  compass-runner. The first four are podman containers. The Server and
  Runner are host processes, and the Server receives the door as
  `--listen-fd 3` (`serverSpec`, `go/internal/stack/spec.go`).
- **Boot order.** `Serve` (`go/server/serve.go`) binds listeners, then
  `openFabricAndStore` opens NATS before the store. `store.Open`
  (`go/internal/store/store.go`) pings once without retry. It then applies
  the embedded migrations under the session-scoped advisory lock
  `migrationLockKey`.
- **RLS roles.** `0001_init.sql` creates `compass_app` (NOLOGIN) and
  `compass_system` (NOLOGIN BYPASSRLS), always re-asserts BYPASSRLS, and
  grants both to the owner. The owner therefore needs CREATEROLE and
  BYPASSRLS (OQ-5).
- **Door.** `loadNetworkTLS` (`go/server/network_door.go`) loads the keypair
  once, with a TLS 1.3 floor and h2. It has no reload. The Server serves no
  UI files. `ServeConfig.CORSAllowedOrigin` admits one browser origin.
- **Health.** `go/server/health.go` serves `GET /healthz` and grpc health on
  every door. It reports NOT_SERVING once drain starts. `compass.runner`
  reports SERVING only while a Runner is enrolled.
- **Secrets.** `resolveMasterKey` (`go/server/serve.go`) fails boot without a
  64-hex `COMPASS_MASTER_KEY`. The flake's `compass-server` is a
  `wrapProgram` bash wrapper that puts secretspec on `PATH` (`flake.nix`).
  `run` in `go/internal/secrets/resolver.go` discards secretspec's stderr, so
  a bad provider surfaces only as `exit status 1` under
  "resolve master key". secretspec writes an audit log under
  `$XDG_STATE_HOME`, and a failed write does not block a resolve.
- **State dir.** `--state-dir` defaults to the socket's parent.
  `ensurePrivateDir` (`go/server/socket.go`) creates missing dirs at `0700`
  and leaves an existing dir alone. `warnIfSharedTokenDir` warns when the
  token dir has group or other bits, as a fresh emptyDir root does.
- **Runner.** `renderRunnerManifests` (`tools/runner-manifests/render-core.ts`)
  takes `serverAddr`, which must be absolute `https`, and emits env only, no
  `args`. The renderer has no CA value, so the Runner trusts system roots
  unless `COMPASS_RUNNER_CA` is set (`go/cmd/compass-runner/main.go`). The
  hub holds one Runner (`go/internal/runnerhub/hub.go`). `Run`
  (`go/internal/runner/run.go`) returns an error when the session stream
  drops.
- **Agent egress.** The microVM always arms a default-deny firewall.
  `--egress-allow` (`go/cmd/compass-runner/main.go`) is the only way to open
  a host, and it has no env fallback. `parseEgress` treats an empty value as
  "no host reachable". `isValidHost` (`go/internal/runtime/host.go`) admits
  DNS names and bare IP literals, never `host:port`. Agents call providers
  with a raw key from the provider seed
  (`go/internal/runtime/secrets_materialize.go`).
- **Issuer keys.** `fetchHTTPS` (`go/internal/auth/workload_identity.go`)
  sends no `Authorization` header, and k3s runs with anonymous auth off. A
  `jwksFile` entry never fetches: its keys "change only on rollout".
  `validateRunnerClusters` (`go/internal/auth/runner_clusters.go`) requires
  issuers to be unique only among the configured entries, so the k3s default
  issuer `https://kubernetes.default.svc.cluster.local` is valid.
- **Image lane.** `runner-image/Dockerfile` copies a nix closure onto a
  digest-pinned distroless `static-debian12` nonroot base.
  `publish-runner-image` and `release-runner-image`
  (`.github/workflows/release.yml`) push by digest and re-tag `:vX.Y.Z` by
  digest. `resolveAncestor` (`tools/runner-image/retag-core.ts`) finds the
  newest `:git-<sha12>` publish at or before a commit.
- **Node shape** ([spike findings](https://github.com/RigelBuild/compass/blob/53ab753f089ef1293d66736f247d41d38063dc7f/docs/designs/platform/compass-runner-containerization/spike-findings.md)).
  `TestFullBoot` passed on a bare-metal k3s node. That node had kernel 6.18,
  `/dev/kvm` `0666`, the generic device plugin and `hostUsers: false`. It has
  no AppArmor. The Ubuntu VM run needed
  `kernel.apparmor_restrict_unprivileged_userns=0`.

### Design

**Topology (D1).** Each stack child that runs as a container today becomes
its own pod. The Server container supervises nothing.

| Child | k3s | EKS |
| --- | --- | --- |
| compass-server | Deployment, `replicas: 1`, `strategy: Recreate` | same |
| NATS | StatefulSet, 1 replica, PVC, `DefaultNatsImage`, the stack's R1 file JetStream config | same |
| Postgres | dev-only StatefulSet, PVC, `DefaultPostgresImage` (OQ-9) | external managed Postgres (OQ-5) |
| OTel collector | none: `OTEL_EXPORTER_OTLP_ENDPOINT` points at the operator's collector | same |
| LLM gateway | not deployed in phase one (OQ-3) | same |
| compass-runner | the existing DaemonSet, on one KVM node | same, on bare-metal nodes |

The Server runs one replica because the hub holds one Runner and the
delivery-cutover record keeps a single Server. `Recreate` also keeps two
Servers from migrating at once. Each rollout drops live sessions (OQ-8).

**Server image (D2).** `ghcr.io/rigelbuild/compass-server` follows the Runner
image's shape. `server-image/Dockerfile` uses the same pinned base and copies
the realised `.#compass-server` closure. The image runs as uid 65532, and the
staged symlink `/nix/store/.compass-server` is its entrypoint. No other
compass binary ships. The closure carries secretspec and the wrapper's bash,
plus glibc and tzdata; an image scan sees them. Publishing reuses the Runner
lane (OQ-6). `SERVER_IMAGE_CLOSURE_PATHS` gates it, and the set is listed in
T3.

**Data and migrations (D3).** Migrations stay in Server boot; there is no
Job.

- The DSN must name a direct, session-mode endpoint, because the migration
  lock is session-scoped.
- One replica fits the default pool of 10.
- On k3s, the Postgres superuser is `compass`, its password comes from a
  Secret, and it logs in with `scram-sha-256`. On the first migration that
  superuser creates the RLS roles. The StatefulSet is dev-only (OQ-9); the
  operator backs up its volume and the master key.
- On EKS, the owner role must hold CREATEROLE and BYPASSRLS (OQ-5).
  Operators turn compute suspend off. A suspended database fails the single
  ping, and the pod restarts until it answers.

**Secrets (D4).** The renderer names Secrets but never holds a value.

- `COMPASS_DATABASE_DSN`, `COMPASS_NATS_URL` and `COMPASS_S3_*` come from
  Secrets as env.
- Server secrets come from one Secret key, `server.env`, mounted read-only
  at `/etc/compass/secrets/`. The Server reads it through
  `--secret-provider dotenv:///etc/compass/secrets/server.env` (OQ-2). The
  file holds `COMPASS_MASTER_KEY` and the forge `SERVER_*` names. A wrong path
  or a bad file fails boot with only `exit status 1`, because secretspec's
  stderr is discarded; the README says to check the mount first.
- `XDG_STATE_HOME` is `/run/compass/xdg-state`, on the emptyDir, so
  secretspec's audit log has a writable home on a read-only root.
- An init container copies the admin token into a `0600` file (OQ-1).
- The operator's infra repo creates every Secret with its own tooling. It
  also backs up the master key, because losing it is unrecoverable
  (`docs/self-host.md`).
- Agent provider keys are not Server secrets. An admin stores each one with
  `compass secret set <NAME> --kind provider --provider <id> --delivery file`,
  value on stdin. `--delivery file` matters: `Install` routes env delivery
  before kind, so an env-delivered key never reaches the provider seed.

**NATS access (D5).** The stack's NATS has no auth because it listens only on
loopback. In a cluster, a NetworkPolicy admits port 4222 only from the
Server pod. NATS also requires a user and password. The password reaches the
NATS config through a variable reference, and the credentials ride in
`COMPASS_NATS_URL`. The fabric's connect path already keeps URL credentials
out of its errors (`go/internal/fabric/fabric.go`).

**Door, TLS and serverAddr (D6).** The Server terminates its own TLS. No
ingress may terminate it: the door refuses cleartext, and the Runner's bidi
stream needs h2 end to end.

- **Certificate.** cert-manager issues a `kubernetes.io/tls` Secret from the
  cluster's existing private-CA ClusterIssuer (OQ-4), with a long duration.
  Its SANs cover the in-cluster name and the external name.
- **Renewal.** The door loads its keypair once. On renewal the operator's
  reload annotation restarts the Server, which drops live sessions (OQ-8).
  In-process reload is deferred (follow-up F1).
- **Services.** Service `compass-server` (ClusterIP) maps port 443 to
  container port 50052.
- **serverAddr** is `https://compass-server.<namespace>.svc`. The Runner
  trusts the CA through a new `serverCA` value (T5).
- **External access** is a LoadBalancer Service on 50052, passed through as
  TCP, at an operator-owned DNS name.
- **Clients.** The CLI uses `--server-addr`, `--ca` and the admin token
  (`go/cmd/compass/client.go`). The desktop app uses client mode with
  `ca_cert` (`docs/self-host.md`). A browser origin comes from the
  `corsAllowedOrigin` value.
- **Probes.** The startup and readiness probes are HTTPS `httpGet` on
  `/healthz`. Readiness never depends on `compass.runner`, because the
  Runner needs the Service before it can enroll. There is no liveness
  probe.

**In-cluster Runner (D7).** Three values make a rendered Runner useful.

- **Identity.** The k3s entry keeps the cluster's default issuer and uses
  `jwksFile`. The operator's infra repo reads the public keys with
  `kubectl get --raw /openid/v1/jwks` at render time, and the Server renderer
  writes them into the runner-clusters ConfigMap. This needs no Go change and
  no anonymous discovery. A k3s signing-key rotation is a manual operator
  step, and it then needs a re-render and a Server rollout
  (`docs/self-host.md`). An EKS issuer serves discovery anonymously and uses
  `jwksURI` discovery as today. `tokenFile` is deferred (follow-up F2).
- **CA.** `serverCA` mounts the private CA and sets `COMPASS_RUNNER_CA`.
- **Egress.** `egressAllow` renders the Runner container's
  `args: ["--egress-allow=<comma-joined hosts>"]`. The renderer applies the
  `isValidHost` grammar, so `host:port` fails at render time, not at Runner
  start. Phase one lists the provider API host, `github.com` and
  `api.github.com`.

**Delivery split (D8).** Public compass holds `tools/server-manifests/`, in
the shape of `tools/runner-manifests/`: values in, plain objects out,
digest-only images, no Secrets. The operator's infra repo holds the values
for each cluster. It renders at a pinned compass commit and commits the
output, and Flux applies it. It also owns Secrets, the Certificate, the
device plugin and node setup. Both image digests resolve from one compass
commit: each is that image's newest publish at or before the commit, so the
Server and Runner API versions match. A scheduled job in the operator's
infra repo moves the pin (OQ-10).

**Rollout (D9).** k3s comes first, as the dev environment. It is done when
three things hold:

- Server and Runner run there.
- A session started from an out-of-cluster CLI completes, with its agent
  reaching the provider and the forge.
- Dev points at the cluster, on a fresh database (OQ-11).

EKS follows with the same renderers and other values. Managed-plane detail is
out of scope; see `docs/concepts/self-host-and-managed.md` and the
[OSS-core boundary record](https://github.com/RigelBuild/compass/blob/53ab753f089ef1293d66736f247d41d38063dc7f/docs/designs/meta/oss-core-managed-boundary/design.md).

## Alternatives considered

- **One pod running `compass-stack up`.** Rejected. The supervisor drives
  podman and host processes, which is the host-level bring-up the
  distribution record keeps.
- **Ingress-terminated TLS, re-encrypted to the door.** Rejected. It adds an
  h2 proxy hop and a second certificate.
- **A migration Job.** Rejected. No migrate-only entrypoint exists, and the
  advisory lock already serializes migrations.
- **In-process TLS reload now.** Deferred (F1): every deploy drops sessions
  the same way (OQ-8).
- **A unique k3s issuer plus `tokenFile`.** Deferred (F2): a live issuer
  change needs a dual-issuer window, and it is a Go change.
- **Anonymous discovery via AuthenticationConfiguration.** Not taken: it needs
  Kubernetes ≥1.34 and an API-server change.
- **A `COMPASS_EGRESS_ALLOW` env fallback.** Not taken: a Go PR that `args`
  makes unnecessary.

## Plan

### Global Constraints

- **Public repo.** No operator names, hosts, zones or private-product
  detail. See `docs/concepts/self-host-and-managed.md`.
- **PRs.** T3, T4 and T5 are independent PRs off compass `main`; none
  stacks on another. Each passes alone, red then green. T6 and T7 live in the
  operator's infra repo.
- **Images.** Digest only. Renderers reject tags, as
  `assertRunnerImageDigest` does. The init image is a value, digest-pinned
  and checked the same way.
- **Secrets.** Renderers emit no Secret object and no secret value.
- **Version coupling.** Server and Runner digests come from one compass
  commit.
- **Kubernetes.** ≥1.33, for user namespaces and for the identity record's
  ≥1.32 floor.
- **Server pod.** uid 65532, `readOnlyRootFilesystem`, drop ALL,
  `RuntimeDefault` seccomp. The socket, state dir and `XDG_STATE_HOME` live
  on one emptyDir at `/run/compass`; the state dir is a child the Server
  creates at `0700`.
- **Lanes.** TypeScript is Bun with `bun:test`, as in
  `tools/runner-manifests/`. Go DB tests use `pgtest`.
- **Spend.** Every new usage-billed meter on EKS ships with an enforced cap
  per layer, a budget alert that pages, and a row in the operator's spend-cap
  register, in the same change.

### Order

```text
T3 server image ──┐
T4 server renderer ┼──► T6 k3s wiring + bump job ──► T7 EKS values
T5 runner values ──┘
Deferred, off the critical path: F1 door TLS reload, F2 tokenFile
```

T3 must merge and publish on `main` before T6 can deploy. T4 and T5 can
merge in any order.

### T3 — Server image and publish lane (CI, nix)

In flight as compass PR #2355. Its smoke runs the locally built OCI image:

- `--version` prints `compass-server <version>`.
- Starting with no DSN exits 1 and names the missing DSN.
- The image config user is `65532:65532`.
- With Postgres and NATS containers, a read-only root and
  `--secret-provider dotenv:///secrets/key.env` from a read-only mount, boot
  resolves the master key and WhoAmI answers. NATS is required, because the
  Server opens the fabric before the store. This mirrors
  `tools/flake-gate/compass-server-secretspec.nix`.
- Two builds give the same manifest digest.

`SERVER_IMAGE_CLOSURE_PATHS` must cover every input of the closure:
`go list -deps ./cmd/compass-server` reaches `go/cmd`, `go/events`, `go/gen`,
`go/internal` and `go/server`, and the secretspec pin comes from
`devenv.lock`. `go/**` is a superset; a Runner-only Go change then publishes
a Server image too, which is the safe direction.

```text
server-image/{Dockerfile,moon.yml,.dockerignore}   // pinned base from runner-image/Dockerfile
tools/server-image/{build.ts,build-core.ts}         // ENTRYPOINT_LINK = ".compass-server", IMAGE_USER = "65532:65532"
tools/runner-image/image-lane-core.ts               // imageLaneConfig(lane: string | undefined): ImageLaneConfig | undefined
tools/runner-image/{publish.ts,retag.ts}            // --lane runner|server
.moon/workspace.yml                                 // compass-server-image: 'server-image'
.github/workflows/release.yml                       // jobs publish-server-image, release-server-image
SERVER_IMAGE_CLOSURE_PATHS:
  server-image/**  tools/server-image/**  tools/runner-image/**  go/**
  tools/toolchain/secretspec-env.nix  devenv.lock  flake.nix  flake.lock
  version.txt  .github/workflows/release.yml
image: ghcr.io/rigelbuild/compass-server@sha256:<digest>, :git-<sha12> and :vX.Y.Z by digest
```

### T4 — Server manifests renderer (TypeScript)

Tests use golden output plus invariants:

- `replicas: 1` with `Recreate`.
- Digest-only images, the init image included.
- No `Secret` kind.
- The NetworkPolicy admits 4222 only from the Server label.
- Probes hit `/healthz`; no liveness probe.
- An absent `postgres` value renders no Postgres objects.
- The pod security context is as listed in Global Constraints.
- `XDG_STATE_HOME` and `--state-dir` sit on the emptyDir, and the state dir
  is not the emptyDir root.
- A `runnerClusters` entry with `jwks` renders a ConfigMap key and a
  matching `jwksFile`; an entry with both `jwks` and `jwksURI` is rejected.

The README documents the render steps, the JWKS read, and the
`exit status 1` boot failure.

```text
tools/server-manifests/render-core.ts
  export type RunnerClusterValues = {
    name: string; issuer: string; audience?: string;
    namespace: string; serviceAccount: string; maxTokenLifetime?: string;
    jwksURI?: string; jwks?: string;   // jwks: JWKS JSON text; renders jwksFile
    caFile?: string;
  }
  export type ServerDeployValues = {
    namespace: string; serverImage: string; natsImage: string; initImage: string;
    postgres?: { image: string; storage: string; secretName: string };   // dev-only
    natsStorage: string; tlsSecretName: string; dsnSecretName: string;
    natsSecretName: string; serverSecretsSecretName: string;   // key server.env
    adminTokenSecretName: string; s3SecretName?: string;
    corsAllowedOrigin?: string; otlpEndpoint?: string;
    external?: { port: number; loadBalancerClass?: string };
    runnerClusters: RunnerClusterValues[];
  }
  export function parseServerDeployValues(raw: unknown): ServerDeployValues
  export function renderServerManifests(v: ServerDeployValues): ReturnType<typeof renderRunnerManifests>
  export function assertServerImageDigest(ref: string): void
  // objects: ServiceAccount, Deployment, Service, Service(LoadBalancer)?,
  //   ConfigMap runner-clusters (clusters.yaml + <name>.jwks.json),
  //   NATS StatefulSet+Service+ConfigMap, Postgres StatefulSet+Service?, NetworkPolicy x2
  // init container (initImage, uid 65532): cp the admin token to
  //   /run/compass/bootstrap/admin-token, chmod 0600
  // server args: --listen 0.0.0.0:50052 --tls-cert /etc/compass/tls/tls.crt
  //   --tls-key /etc/compass/tls/tls.key --state-dir /run/compass/state
  //   --socket /run/compass/state/server.sock
  //   --admin-token-file /run/compass/bootstrap/admin-token
  //   --runner-clusters /etc/compass/runner-clusters/clusters.yaml
  //   --secret-provider dotenv:///etc/compass/secrets/server.env
  // env: XDG_STATE_HOME=/run/compass/xdg-state
tools/server-manifests/{render.ts,values.example.json,README.md,moon.yml}
.moon/workspace.yml                  // server-manifests
```

### T5 — Runner renderer `serverCA` and `egressAllow` (TypeScript)

When `serverCA` is set, the renderer mounts the ConfigMap key and sets
`COMPASS_RUNNER_CA`. When `egressAllow` is non-empty, the container gets one
`--egress-allow=` arg with the hosts comma-joined. Invalid hosts fail
`parseRunnerDeployValues`. With both absent, the output is unchanged. Tests
cover each case and a `github.com:443` rejection. The README covers both
values and lists the hosts a session needs.

```text
tools/runner-manifests/render-core.ts
  RunnerDeployValues.serverCA?: { configMapName: string; key: string }
  // mount /etc/compass/server-ca/<key>; env COMPASS_RUNNER_CA
  RunnerDeployValues.egressAllow?: string[]
  // args: ["--egress-allow=" + hosts.join(",")]; grammar of go/internal/runtime/host.go isValidHost
  export function assertEgressHost(host: string): void
```

### T6 — k3s wiring in the operator's infra repo (operator infra)

Node, declared in the host's config as code:

- Promote the spike's generic device-plugin slice to its permanent place.
- The `/dev/kvm` `0666` udev rule.
- The seccomp profile staged at the kubelet root.
- Confirm Kubernetes ≥1.33. No issuer change and no AppArmor sysctl: the
  KVM node has no AppArmor.

Cluster:

- One Certificate against the existing private-CA ClusterIssuer, with a long
  duration and the reload annotation on the Server Deployment.
- The Secrets from D4, including `server.env`.
- The k3s JWKS read into the values.
- Rendered output is committed. Flux Kustomizations apply data, then
  Server, then Runners.
- A scheduled bump job (OQ-10). For one compass commit it resolves each
  lane's digest with `resolveAncestor` from
  `tools/runner-image/retag-core.ts`, re-renders both, and opens a pull
  request.

Dev move (OQ-11): a fresh install. The admin re-enters forge and provider
secrets with `compass secret set`, provider keys with `--kind provider
--delivery file`, and pushes agent configs again.

Acceptance:

- Flux reports Ready.
- `/healthz` returns 200.
- `compass.runner` reports SERVING.
- An out-of-cluster CLI session completes, and its agent reaches the
  provider and pushes to the forge.
- Dev points at the cluster.
- The bump job opens one pull request for a new compass commit.

```text
inputs: tools/server-manifests + tools/runner-manifests at one compass commit
Runner values: serverAddr https://compass-server.<ns>.svc, serverCA set,
  egressAllow [<provider API host>, github.com, api.github.com]
Server values: runnerClusters[0] = { issuer: https://kubernetes.default.svc.cluster.local, jwks: <JWKS> }
```

### T7 — EKS values (operator infra)

First confirm OQ-5's owner-role attributes. Then wire:

- The external Postgres direct endpoint, with compute suspend off.
- A passthrough NLB.
- The cluster's public issuer with `jwksURI` discovery.
- A bare-metal node group that exposes `/dev/kvm`, sized for the Runner's
  `sessionCapacity`. Every Runner pod requests one `kvmResourceName` device.
- For each new meter (managed Postgres, the NLB, bare-metal nodes): an
  enforced cap at each layer, a budget alert that pages, and a spend-cap
  register row, in the same change.

Acceptance matches T6, without the dev move.

### Deferred follow-ups

- **F1 — Door reloads its TLS keypair (Go).** Taken when OQ-8 option 2 is
  ruled out or renewals become frequent. Tests: after a rewrite the next
  handshake presents the new serial; a corrupt rewrite keeps the old serial;
  existing connections survive.

```text
go/server/network_door.go
  func newCertReloader(certFile, keyFile string, log *slog.Logger) (*certReloader, error)
  func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
```

- **F2 — `tokenFile` on a Runner cluster (Go).** Taken when a cluster needs
  live key rotation without a rollout. Key fetches send
  `Authorization: Bearer` with the file's contents, re-read on every fetch.

```text
go/internal/auth/runner_clusters.go
  RunnerCluster.TokenFile string   // yaml:"tokenFile", optional
go/internal/auth/workload_identity.go
  func fetchHTTPS(ctx context.Context, client *http.Client, u, bearer string) ([]byte, error)
```

## Tasks

- [ ] T3 — Server image and publish lane (CI, nix), compass PR #2355
- [ ] T4 — `tools/server-manifests` renderer (TypeScript)
- [ ] T5 — Runner renderer `serverCA` and `egressAllow` (TypeScript)
- [ ] T6 — k3s wiring, bump job and dev move (operator's infra repo)
- [ ] T7 — EKS values, bare-metal nodes and spend caps (operator's infra repo)
