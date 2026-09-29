# IMG-00 baseline

Resource baseline a4ca2a0fcb18346fff27f682a5244874ca4c60c8; Governance actual d1a804f1d35d7294cb7eab48ee3f256d9d2482b5 (newer than design). Local Resource was clean; Governance `_agent/` preserved in original checkout, implementation uses separate worktree. Review branches only.

Fedora read-only probe: host=fedora, HOME=/home/chabking, Go=go1.26.7-X:nodwarf5, systemd user manager running, historical lock exists and bounded acquisition succeeded, 200% CPU/2300M/0 swap scope command exit 0. Home filesystem 389G free, memory available ~10012MiB; existing swap heavily occupied, no swap budget added. New run root in source-lock, mode 0700. PG runner exists as `scripts/integration`, pinned digest and 768MiB/1CPU; no DB started yet.

No shared cluster, Harbor, credentials or business DB accessed. Live profile requested asynchronously; missing inputs block live only. Consumer binding remains blocked, frontend identified; no ANI source read. No runtime implementation or acceptance claimed by importing plans.

GOAL source-first commit/push cycle takes precedence over AGENTS pre-commit verify ordering; make verify remains required on resulting generated candidate.
