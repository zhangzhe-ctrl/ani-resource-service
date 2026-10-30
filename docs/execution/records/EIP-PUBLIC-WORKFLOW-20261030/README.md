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
