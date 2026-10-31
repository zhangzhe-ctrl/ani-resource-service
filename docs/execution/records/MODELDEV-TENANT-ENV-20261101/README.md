# Resource implementation: dynamic tenant environment prerequisites

Base: `23e25b33ca02890231cd273ef7246b0fc415476c` (main read through
`ssh://git@ssh.github.com:443/zhangzhe-ctrl/ani-resource-service.git`).
Implementation branch: `codex/modeldev-dynamic-tenant-environment`.

The Resource component accepts only an existing standard tenant Namespace and
its original UID. Network acceptance, provider operations and VM NAD reads
reject missing/changed/NULL identity; Sandbox no longer initializes Namespace
or bypasses the guard. Storage validates Namespace identity on replay and
checks the live UID before writes. VM/Instance selection uses the fixed name.
ModelDev pull Secret authorization is dynamic under tenant/cluster/name/UID
checks; Inference retains its static branch and no SA writer was added.

Migration 0025 and `scripts/tenant-namespace-backfill` prepare the task 09
maintenance window. The command defaults to a read-only plan, requires a reviewed
SHA256 for apply, conditionally updates only exact existing NULL rows, preserves
existing UIDs and unrelated clusters, and reports before/after identities/counts.
No production migration, backfill, deployment or cluster write was run here.

| Check | Result | Actual evidence |
|---|---|---|
| NS01 canonical/public positive-negative vector | PASS (component) | Pure helper test; vector SHA256 `1e7eb5561043b5fe172e8bcf18a173cd3b2deb90804ded802a55ed06d1bc882a`; [verify](verify.log) |
| NS02 existing name/owner/phase/UID and no Namespace Create/Patch | PASS (component) | Network/Sandbox/VM/Instance fake Kubernetes actions and ModelDev Secret writer boundary tests; [verify](verify.log) |
| Storage replay still validates identity | PASS (component) | `TestCreateVolumeReplayStillValidatesNamespaceIdentity`; [verify](verify.log) |
| RS01 concurrent different UID admission conflict | PASS (real isolated PostgreSQL) | `TestTenantNamespaceConcurrentAdmissionRejectsUIDConflict`; [PG](namespace-pg.log) |
| RS01 NULL runtime refusal, exact NULL backfill, existing UID and unrelated cluster preservation | PASS (real isolated PostgreSQL) | `TestTenantNamespaceBackfillIsExactAndNeverOverwritesUID`; [PG](namespace-pg.log) |
| IM01 dynamic ModelDev Secret target / cross tenant-cluster-UID-registry rejection; Inference original caller regression | PASS (component) | Real use case and adapter tests; [verify](verify.log) |
| Pinned config/protobuf/sqlc generation | PASS | Buf 1.60.0, sqlc 1.31.1; [generation](generation.log), deterministic generation in [verify](verify.log) |
| Repository required `make verify` | PASS, exit 0 | Fedora, `GOTOOLCHAIN=auto GOMAXPROCS=2 GOFLAGS=-p=2 make verify`; generation, boundaries, fault builds, tidy diff, default tests, vet/build, module verification and diff checks in [verify](verify.log) |
| Runtime source matches verified Fedora source | PASS, exit 0 | 880 input files in [manifest](source-files.sha256), manifest SHA256 `f26ffcafb6a4d5c11acb152d8e2b0044d26441e9d4fb5ff3a9c3135b9a3a3239` |
| Live UID backfill/zero NULL, deployment and A/B business acceptance | NOT_RUN | Owned by task 09/10; this record makes no live claim |

All generation, builds, tests and isolated PostgreSQL ran only on `ssh fedora`
in `/home/chabking/workspace/ani-resource-service-runs/modeldev-tenant-env-20261101/source`.
Source transferred with rsync, with no Fedora packaging or archive transfer.
The PostgreSQL driver used pinned image
`docker.io/library/postgres@sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280`
and removed its task-owned container. Default `make verify` skips opt-in PG/live
cases; only the two named PG cases above were separately selected. No full
integration or live suite was run.

The first mandatory verification identified two config-field references and a
real historical read regression: Instance metric scope had used the create
namespace resolver merely for DNS syntax validation. Tightening creation would
reject unchanged persisted historical scopes. The minimum correction separates
read-only name syntax validation from new target authorization, preserving
original tenant-scoped Instance/VM metric evidence as required by spec 2.2/12.3.
The final required verification passed after that correction. Startup rejects
old nonempty namespace-prefix configuration; ModelDev caller's old namespace/
owner-label configuration must also be removed at task 09 switch. Existing
Inference configuration is retained.
