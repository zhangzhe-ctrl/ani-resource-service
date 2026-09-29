# Image MVP continuation checkpoint

Recorded 2026-09-29T21:14Z. Current authority remains Resource `docs/execution/status.md`.
Goal is active and incomplete; no claim of completion or work after an agent session ends.

- Resource runtime code verified at `62ecc67b839dd1040468323f8c316189a0c30653`; latest pushed documentation `e3142cf5ea891a7439945ef12de33ff687f8b5d6`.
- Governance clean pushed `d64d6ee478801795afadb4573ba25f8c2de6b7fc`; default build/HTTP/old data-service-server regressions pass. Default optimized race OOM retained; full race instrumentation with only generated Ent inlining disabled passed. Details in IMG-06-governance.
- Console independent worktree `/home/chabking/workspace/.worktrees/console-image-mvp-20260930`, local preparation commit `674f2f0b09be0faf2b36ff334f291ad2575e21d7`, original master untouched. SSH push denied to zhangzhe-ctrl, HTTPS stalled own Git terminated exit143; remote review branch absent. Bundle transferred only, no unpushed generation/test. Writable frontend input requested asynchronously.
- IMG-07 CLI/platform/smoke code and isolated gates pass; evidence archived. Live profile remains unapproved. Smoke checks reject it before workdir or network. No shared Harbor/cluster writes.
- IMG-08 actual ordinary-container owner/Create/tenant Namespace binding remains missing. Do not infer ownership from KServe, BOSS or old ANI. No ANI code read or modified.

## Active remote work; resume these, do not start duplicate gates

Fedora root `/home/chabking/ani-image-mvp-runs/img-20260929T1718Z`; shared lock `/home/chabking/workspace/ani-network-service-runs/net05a-heavy.lock`.

`state/resource-final-regression.sh` is running under scope `run-p909592-i9314307.scope`, started21:03:04Z on exact62ecc67; tool exec session89070. It runs original `make integration`, `make race`, `make tenant-mutations` sequentially, then protected-byte diff and exact fixture absence checks. CPU200%/2300M/swap0, task caches and TMPDIR `/home/chabking/.im-1718`. Original scripts unchanged; exported execution wrapper adds PG memory-swap=768m and own run label, verifies ownership before their cleanup. At21:13Z `internal/data/network` integration was still running; no final exit. Do not call this pass yet. PostgreSQL initial ID `97e877ed1e2bc9db309761962179ec650b5c7c80e723fe8d64e10d9d7cb67f9d`. Read-only diagnosis showed active requests, no PG lock waits, scope peak~1.02GiB. Test has original20m timeout. An extra read-only psql diagnosis had SQL quoting error exit1; no mutation/source impact.

The unchanged legacy tenant-mutation script requires `.tools/bin/sqlc`; `state/network-sqlc-link.sh` added an ignored symlink in this exact owned checkout to pinned task sqlc1.31.1. It verified clean HEAD and tool version, no tracked edit. Raw setup `evidence/IMG-10/62ecc67...-network-regression/sqlc-tool-path.txt`.

`state/review-docs-run.sh` is queued on the SAME flock (tool session40188), not executing concurrently. It will check pushed docSHAe3142cf, compare all post62ecc67 changes against documentation allowlist, verify current Image contract equals the Governance dependency71aa986, protected Network paths/checksums, and document link targets/anchors. It uses local-authored `state/review-docs-check.py`. No code generation or test on Console preparation.

## Next safe steps

1. Wait for actual Network gate exit; if failure, retain original logs and diagnose/reproduce relevant failing case, without lowering gates or changing unrelated Network behavior. Archive exact commands/log hashes/IDs/cleanup. Resume queued document checker and fix only actual document defects.
2. Finish source/acceptance snapshot, update HANDOFF and the sole ledger with real Network results and cleanup; document all blocked/not_run live/product tasks. Verify final post-test diff remains documentation-only; push normal review commit.
3. Missing inputs: approved live profile; actual non-ANI ordinary-container owner/Namespace resolver; writable frontend target. Resume corresponding blocked batches only after those are resolved. Do not invent a runtime listener/owner or publish to an unapproved alternate frontend fork.
4. Keep all initial dirty work, local worktrees, Fedora snapshots/caches/evidence. Only exact owned test containers may be cleaned; no shared resource cleanup or global GC.
