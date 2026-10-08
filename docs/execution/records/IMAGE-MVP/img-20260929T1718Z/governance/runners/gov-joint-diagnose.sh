#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
export TMPDIR="$HOME/.im-1718" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1536MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org
exec > >(tee "$root/evidence/IMG-06/gov-9690a811d257d2ff88e835bc064e1affb3bb126d-verify/joint-before.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"' EXIT
cd "$root/repo/governance/9690a811d257d2ff88e835bc064e1affb3bb126d"
date -u +%FT%TZ
pwd
test -z "$(git status --porcelain)"
scripts/image-joint-integration
