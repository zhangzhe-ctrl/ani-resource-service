# Public EIP workflow evidence (2026-10-30)

Source base: Resource main `bf1b0342267f0e5bedcca1728f1fa918bc17e9ed`.
Implementation branch: `codex/eip-public-workflow`.
Runtime source: [825 exact file hashes](runtime-source.json), manifest SHA-256
`f4cadf8da095211dbdeec53280cf264ca9bb312e9858c7ad6f29c3f54c613707`, generated on Fedora
and returned with an input-hash guard. No KCN repository changes.
Authoritative behavior: [public egress contract](../../../specs/vpc-connectivity-lb.md#5-public-eip-与-vpc-公网出站调整)
and [security groups](../../../specs/security-groups.md). Current state is only
[the execution ledger](../../status.md#public-eip-workflow-20261030).

## Commands and boundary

Host: `ssh fedora`; isolated source:
`/home/chabking/task-runs/eip-public-workflow-20261030/resource`.
Tool environment:
`/home/chabking/workspace/ani-resource-service-runs/remove-nodefacts-20261030/env.sh`;
all generation/build/test/PG uses the shared
`/home/chabking/workspace/ani-network-service-runs/net05a-heavy.lock`.
No local execution fallback.

`make generate` passed with Go 1.26.10 linux/amd64 and pinned Buf 1.60.0 / sqlc 1.31.1.
`make verify` passed: reproducible generation, boundaries, fault builds,
mod tidy, package tests, vet, build, module verification and whitespace check
([complete log](resource-verify.txt)).
The focused PostgreSQL run passed in 15.658 seconds; [full command output](resource-pg.txt).
Command:

```bash
scripts/integration -timeout 8m -run '^TestPublicEIPWorkflow|^TestSecurityGroupsPostgres|^TestBaseSchemaEmptyMigrationAndRuntimeRole|^TestBaseSchemaLegacyPublicUpgradePreservesDurableFactsAndReplay|^TestSubnetMigrationUpgradesNET01WithoutChangingDurableFacts|^TestNET05AMigrationUpgradesAllThreeHistoricalChecksums|^TestPlatformEIP|^TestLBConcurrentSNATClaimBothArrivalOrders|^TestEgressKCAdapterSharedObservationLifecycleAndDrift' -v ./internal/data/network
```

The use cases, SQL, claim uniqueness, idempotency, references, leases, shared worker,
Provider adapter and projections are real code. Only Kubernetes/controllers are external boundary doubles: public lifecycle
tests traverse the actual KC adapter over controlled HTTP, while the atomic SG
transaction test replaces only its external Provider port. This is not a live
dataplane result.

Observed behaviors include:

- VPC with zero base rows binds a public EIP, disables, re-enables, unbinds and
  deletes normally. A base-provider fault remains visible and does not block
  public bind or re-enable.
- The actual KC adapter rejects an unapplied VPC generation and recovers via
  normal observation. UID, freshness, pending mutation, deletion and generation
  admission fences remain effective, and failed admission leaves no claim.
- VPC get/page and EIP get expose the same active claim; disabled/stale/deleting
  states retain occupancy, stale applied state is unknown, and confirmed unbind
  clears both projections.
- Initial EIP SG selection is persisted atomically. A foreign group rolls back
  the EIP and acceptance key, reversed set order replays the same receipt, a
  changed selection conflicts, references protect groups, and existing EIP
  configuration can be replaced using its independent version.
- Empty and historical schemas migrate without changing old facts; new Provider
  proof defaults to unknown. Existing platform EIP inventory, SNAT/LB claim
  contention and KC public lifecycle regressions pass.

## Corrections discovered by the run

The first SQL CASE projection needed an explicit jsonb cast for typed sqlc output.
The first generation guard used the reconciliation wakeup counter; base-child
notifications also advance that counter. The final implementation instead records
actual Provider metadata generation and observed generation before base aggregation.
Historical snapshot checks explicitly exclude only newly added proof fields and
assert unknown defaults. The no-base case uses an actual no-base VPC fixture,
rather than the historical helper that deliberately seeds base data.

The existing EIP parent readiness gate is retained: after SNAT disable is applied,
EIP observation may need another cycle to confirm its current binding generation.
The flow waits for that real EIP observation before re-enable; errors now distinguish
VPC Provider readiness from EIP readiness. No persisted Ready facts are fabricated.

Tagged joint fixture binary:
`/home/chabking/task-runs/eip-public-workflow-20261030/resource-network-joint.test`,
compiled with `go test -tags=networkintegration -c`. Joint HTTP and live release
results are owned by the cross-service integration run; they are not implied by
this Resource-only record. Real packet forwarding is not established by these tests.

## Cross-service and 10-cluster continuation

Runtime revisions: Resource `eec4debf113c6d7df4cd187e0e0fafad0d18ae6a`,
Governance `9bbf87bc1314ce1aadfe69b32ada8f533eaa0ff3` with formal Resource SDK
`v0.0.0-20261030080854-eec4debf113c` (GOWORK=off, no replace).
Console `d0ae1671e5f6062a623591e6cdb727c7e77249fa` is previewed from its isolated,
validated Fedora source at `http://172.16.101.31:5173/eips`. Console PR #6 was still
open when checked, so the EIP increment is stacked on its `codex/security-groups`
branch rather than replacing it.

The focused Governance joint run passed in 37.99 seconds (security_groups subcase
32.17 seconds), traversing real JWT/AK, permissions/subscription, mTLS, PostgreSQL,
Resource worker and KC adapter. Only Kubernetes/controllers were an external
boundary double. Formal-SDK unit tests, tidy check and static server build passed.

10-cluster identity: `5277649d-e28d-4a0a-ae76-8354365c63bc`, nodes .10/.11/.12.
Migration 0024 and image-only guarded Deployment updates passed; original runtime
configuration was preserved. Immutable deployment receipts:
[Resource](ani-resource-service-result.json), [Governance](ani-governance-result.json),
[migration](migration-result.json). Runtime images are respectively
`sha256:fc83a89c25e75d898b68c6681f2bf1db48027507ce2edbaa3a6cd973093a8b56`
and `sha256:f30ac3983eb2a5f28c4efbe5ecbd3e710686b466c6f99b26ceaaee0faa9b289d`.

The user-approved public pool `pool_1c5c2bebf286440fa7ed5006c2256021`
(`cluster-public-102-res-20261022`, 172.16.102.0/24) was enabled for allocation and
set as default, and is retained for page use. Neither operation depends on a base
Intranet EIP. Default-selection's first expected-version conflict was retried
against a fresh GET; [receipt](pool-enabled.json) and [operation log](pool-enable.txt).
An initial EIP request returned PUBLIC_EGRESS_NOT_READY; after fresh real
observations the same unaccepted key succeeded. No readiness facts were injected
and the transient cause was not established.

The real task VPC had [zero base rows](live-no-base-before.txt); its own Provider
proof was populated by [normal observation](live-own-vpc-proof.txt). Actual EIP
172.16.102.193 was created with the initial SG in the acceptance transaction;
identical retry returned the immutable receipt. Public bind, both association
projections, disable, re-enable, SG replacement/clearing and confirmed unbind
passed: [bound VPC](live-bound-vpc.json), [bound EIP](live-bound-eip.json),
[unbound views](live-unbound.json), [result by layer](live-remaining-result.json).
The first flow stopped on missing EIP Ready ([log](live-flow-02.txt)); a subsequent
continuation proved the public lifecycle and raw KCN membership, then its access
token expired after unbind acceptance ([log](live-remaining.txt)). Normal password
login renewed the task session before cleanup; no auth bypass was used.

Nonempty SG application is **not verified end to end in the old cluster**:
[original live YAML](live-eip-sg-before-change.yaml) has SG Ready=True and matching
appliedEIPs, but the EIP has Initialized/Valid=True without Ready. Controller
startup logs self-report `git-ca06a4e`, matching the old success path with no
SetReady(True). The later KCN fix is `971f882d1e7dc21226e4e6d6393f619caf487d6b`,
already included by local KCN `6132623baabee50f17e9e179c0f5e9c1f380db04`.
The binary self-report is not signed image-to-source provenance. Per user direction,
old-cluster diagnosis stops; new-code-cluster retesting is user-owned. Resource
retains the normal Ready gate; no KCN changes or compatibility fallback were added.
Raw KCN membership snapshots: [initial](live-kcn-initial.json),
[replaced](live-kcn-replaced.json), [cleared](live-kcn-cleared.json).
Browser clicking and real packet forwarding are NOT_RUN.

Final [product cleanup states](live-cleanup-state.json) confirm the task EIP, VPC
and both SGs are deleted; the SNAT was already confirmed deleted. The enabled,
default public pool is retained as explicitly requested. No finalizers or database
state were forced. The original user's VPC and unrelated runtime were not changed.
