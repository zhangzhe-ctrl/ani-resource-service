#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
evidence="$root/evidence/IMG-01/socket-path-repro"
mkdir -p "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
export GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1536MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC
cd "$root/repo/resource-baseline"
git rev-parse HEAD
set +e
TMPDIR="$root/cache/tmp" go test -count=1 ./scripts/lb-api -run '^TestSocketRequiresPrivateDirectoryAndPreservesExistingPath$'
rc=$?
set -e
echo "baseline-long-tmp-exit=$rc"
test "$rc" -ne 0
# This directory is owned only by this run; no symlink or /tmp fallback.
test ! -e "$HOME/.im-1718"
umask 077
mkdir "$HOME/.im-1718"
export TMPDIR="$HOME/.im-1718"
printf 'short_task_tmp=%s\n' "$TMPDIR"
go test -count=1 ./scripts/lb-api -run '^TestSocketRequiresPrivateDirectoryAndPreservesExistingPath$'
cd "$root/repo/resource/109ddcc8916fbee2ca5fdcf77e0f38c8cbef30b2"
git rev-parse HEAD
go test -count=1 ./scripts/lb-api -run '^TestSocketRequiresPrivateDirectoryAndPreservesExistingPath$'
