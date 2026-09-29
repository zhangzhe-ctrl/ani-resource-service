#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
sha="$1"
[[ "$sha" =~ ^[a-f0-9]{40}$ ]]
source_dir="$root/repo/resource/$sha"
attempt="${2:-final}"
[[ "$attempt" =~ ^[a-z0-9-]+$ ]]
evidence="$root/evidence/IMG-11/$sha-$attempt"
mkdir "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
if [[ ! -d $source_dir ]]; then
 git clone --no-checkout --shared "$root/repo/resource-baseline" "$source_dir"
 git -C "$source_dir" fetch "$root/inputs/resource-current.bundle" HEAD
 git -C "$source_dir" checkout --detach "$sha"
fi
cd "$source_dir"
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
python3 "$root/state/review-docs-check.py"
sha256sum -c docs/execution/records/IMAGE-MVP/img-20260929T1718Z/network-migrations.sha256
python3 "$root/state/final-snapshot.py" "$evidence/source-snapshot.json"
