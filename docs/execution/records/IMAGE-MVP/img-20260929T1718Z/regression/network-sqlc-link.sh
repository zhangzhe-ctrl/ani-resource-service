#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/resource/62ecc67b839dd1040468323f8c316189a0c30653"
evidence="$root/evidence/IMG-10/62ecc67b839dd1040468323f8c316189a0c30653-network-regression"
exec > >(tee "$evidence/sqlc-tool-path.txt") 2>&1
date -u +%FT%TZ
cd "$source_dir"
test "$(git rev-parse HEAD)" = 62ecc67b839dd1040468323f8c316189a0c30653
test -z "$(git status --porcelain)"
mkdir -p .tools/bin
if [[ -e .tools/bin/sqlc || -L .tools/bin/sqlc ]]; then
 test "$(readlink -f .tools/bin/sqlc)" = "$root/cache/tools/sqlc"
else
 ln -s "$root/cache/tools/sqlc" .tools/bin/sqlc
fi
test "$(.tools/bin/sqlc version)" = v1.31.1
git check-ignore .tools/bin/sqlc
test -z "$(git status --porcelain)"
echo 'Original mutation runner tool path bound to verified task sqlc; no tracked source changed; exit=0'
