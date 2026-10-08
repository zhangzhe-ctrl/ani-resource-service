#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
cd "$root/repo/resource-baseline"
mkdir -p "$root/evidence/IMG-00"
exec > >(tee "$root/evidence/IMG-00/preflight.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$root/evidence/IMG-00/preflight.exit"' EXIT
date -u +%FT%TZ
hostname
pwd
git rev-parse HEAD
test "$(git rev-parse HEAD)" = 3d83c7618b861f354ca4e34d8e0a9d09cb2bb0b4
test -z "$(git status --porcelain)"
export TMPDIR="$root/cache/tmp" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1536MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC
make check-buf check-sqlc TOOLS_DIR="$root/cache/tools"
git diff a4ca2a0fcb18346fff27f682a5244874ca4c60c8 HEAD --exit-code -- migrations api/network internal/conf README.md AGENTS.md
sha256sum migrations/*.sql > "$root/evidence/IMG-00/network-migrations.sha256"
python3 - <<'PY'
from pathlib import Path
import re,subprocess
files=subprocess.check_output(['git','diff','--name-only','a4ca2a0','HEAD'],text=True).splitlines()
count=0
for path in files:
 p=Path(path)
 if p.suffix!='.md':continue
 for link in re.findall(r'\]\(([^)]+)\)',p.read_text()):
  if '://' in link or link.startswith('#'):continue
  target=(p.parent/link.split('#')[0])
  assert target.exists(),(path,link)
  count+=1
print('imported document link targets pass:',count)
PY
