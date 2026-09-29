#!/usr/bin/env bash
set -Eeuo pipefail
sha=d64d6ee478801795afadb4573ba25f8c2de6b7fc
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/governance/$sha-serial"
evidence="$root/evidence/IMG-06/gov-$sha-verify-race-noinline"
mkdir "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'exit 143' TERM
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
cd "$source_dir"
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export TMPDIR="$HOME/.im-1718" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=1 GOFLAGS='-p=1 -trimpath -gcflags=go-wind-admin/app/admin/service/internal/data/ent=-l' GOMEMLIMIT=768MiB GOGC=50 GOTOOLCHAIN=local GOWORK=off TZ=UTC GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org
printf 'GOMAXPROCS=1 GOFLAGS=-p=1,-trimpath,Ent=-l GOMEMLIMIT=768MiB GOGC=50\n'
go test -mod=readonly -count=1 -race ./pkg/middleware/auth ./app/admin/service/internal/data ./app/admin/service/internal/service -run Image
scripts/image-joint-integration -race
test -z "$(git status --porcelain)"
git diff --check
