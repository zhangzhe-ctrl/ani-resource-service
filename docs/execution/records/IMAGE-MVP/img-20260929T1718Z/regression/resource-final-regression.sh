#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
sha=62ecc67b839dd1040468323f8c316189a0c30653
source_dir="$root/repo/resource/$sha"
evidence="$root/evidence/IMG-10/$sha-network-regression"
mkdir -p "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
cd "$source_dir"
date -u +%FT%TZ
hostname
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export TMPDIR="$HOME/.im-1718" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1536MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC
export PATH="$root/cache/tools:$PATH"
export IMAGE_NETWORK_IDS="$evidence/created-containers.txt" IMAGE_NETWORK_OWNER="img-20260929T1718Z-final-regression"
test ! -e "$IMAGE_NETWORK_IDS"
touch "$IMAGE_NETWORK_IDS"
# Preserve the original integration runner bytes and assertions. This local
# execution wrapper only enforces its PG swap budget and verifies own cleanup.
docker() {
 if [[ ${1:-} == run ]]; then
  shift
  local id
  id=$(command docker run --memory-swap=768m --label "ani.image.regression=$IMAGE_NETWORK_OWNER" "$@")
  printf '%s\n' "$id" >> "$IMAGE_NETWORK_IDS"
  command docker inspect --format 'owned_fixture={{.Id}} memory={{.HostConfig.Memory}} memory_swap={{.HostConfig.MemorySwap}} nanocpus={{.HostConfig.NanoCpus}}' "$id" >&2
  printf '%s\n' "$id"
 elif [[ ${1:-} == rm ]]; then
  local id=${@: -1}
  grep -Fxq "$id" "$IMAGE_NETWORK_IDS"
  test "$(command docker inspect --format '{{index .Config.Labels "ani.image.regression"}}' "$id")" = "$IMAGE_NETWORK_OWNER"
  command docker "$@"
 else
  command docker "$@"
 fi
}
export -f docker
if ./scripts/image-smoke --profile "$root/inputs/live-profile.json" --workdir "$root/state/unapproved-smoke-check"; then
 echo 'unapproved live profile unexpectedly accepted'; exit 1
else
 rc=$?
 test "$rc" = 1
 test ! -e "$root/state/unapproved-smoke-check"
 echo 'unapproved live profile rejected before workdir/network; expected driver exit=1'
fi
printf 'gate=make integration\n'
make integration TOOLS_DIR="$root/cache/tools"
printf 'gate=make race\n'
make race TOOLS_DIR="$root/cache/tools"
printf 'gate=make tenant-mutations\n'
make tenant-mutations TOOLS_DIR="$root/cache/tools"
printf 'gate=protected source and cleanup\n'
git diff a4ca2a0 --exit-code -- ':(glob)migrations/*.sql' api/network README.md AGENTS.md internal/data/network/sqlcgen scripts/integration
for id in $(cat "$IMAGE_NETWORK_IDS"); do
 if command docker inspect "$id" >/dev/null 2>&1; then echo "fixture still exists=$id"; exit 1; fi
 printf 'confirmed fixture absent=%s\n' "$id"
done
test -z "$(git status --porcelain)"
