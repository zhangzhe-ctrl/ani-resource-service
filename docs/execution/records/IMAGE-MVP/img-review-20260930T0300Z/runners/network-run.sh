#!/usr/bin/env bash
set -Eeuo pipefail
sha=$1 mode=$2 label=$3
[[ $sha =~ ^[0-9a-f]{40}$ && $label =~ ^[a-z0-9-]+$ ]]
root="$HOME/ani-image-mvp-runs/img-review-20260930T0300Z"
prior="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
cd "$root/repo/resource/$sha"
evidence="$root/evidence/$label-$sha"
mkdir "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export TMPDIR="$HOME/.ir-0930" GOCACHE="$prior/cache/go-build" GOMODCACHE="$prior/cache/go-mod" GOPATH="$prior/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1024MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC
export PATH="$prior/cache/tools:$PATH"
export IMAGE_NETWORK_IDS="$evidence/created-containers.txt" IMAGE_NETWORK_OWNER="img-review-20260930T0300Z-$label"
touch "$IMAGE_NETWORK_IDS"
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
case "$mode" in
 integration) make integration TOOLS_DIR="$prior/cache/tools" ;;
 race) make race TOOLS_DIR="$prior/cache/tools" ;;
 mutations)
 mkdir -p .tools/bin
 test ! -e .tools/bin/sqlc || test "$(readlink .tools/bin/sqlc)" = "$prior/cache/tools/sqlc"
 test -e .tools/bin/sqlc || ln -s "$prior/cache/tools/sqlc" .tools/bin/sqlc
 make tenant-mutations TOOLS_DIR="$prior/cache/tools" ;;
 *) exit 2 ;;
esac
git diff 8f317deef04d034e7b7e0459aacb25a70f01360c --exit-code -- ':(glob)migrations/*.sql' api/network README.md AGENTS.md internal/data/network scripts/integration
for id in $(cat "$IMAGE_NETWORK_IDS"); do
 if command docker inspect "$id" >/dev/null 2>&1; then echo "fixture still exists=$id"; exit 1; fi
 printf 'confirmed fixture absent=%s\n' "$id"
done
test -z "$(git status --porcelain)"
