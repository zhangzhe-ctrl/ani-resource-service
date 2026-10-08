#!/usr/bin/env bash
set -Eeuo pipefail
sha=$1
mode=$2
[[ $sha =~ ^[0-9a-f]{40}$ ]]
[[ $mode == generate || $mode == verify ]]
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/resource/$sha"
evidence="$root/evidence/IMG-02/$sha-$mode${3:-}"
mkdir -p "$evidence" "$(dirname "$source_dir")"
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
export TMPDIR="$HOME/.im-1718" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1536MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC
export IMAGE_ROUND_EVIDENCE="$evidence" IMAGE_ROUND_ROOT="$root"
if [[ $mode == generate ]]; then
 python3 - <<'PY'
import hashlib,json,os,subprocess
from pathlib import Path
files=subprocess.check_output(['git','ls-files','-z']).decode().split('\0')
manifest={f:hashlib.sha256(Path(f).read_bytes()).hexdigest() for f in files if f and not Path(f).is_symlink()}
(Path(os.environ['IMAGE_ROUND_EVIDENCE'])/'before.json').write_text(json.dumps(manifest))
PY
 make generate TOOLS_DIR="$root/cache/tools"
 python3 - <<'PY'
import hashlib,json,os,subprocess,tarfile
from pathlib import Path
root=Path(os.environ['IMAGE_ROUND_ROOT']);ev=Path(os.environ['IMAGE_ROUND_EVIDENCE']);before=json.loads((ev/'before.json').read_text())
files=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z']).decode().split('\0')
changes=[]
for name in files:
 if not name:continue
 p=Path(name);assert not p.is_symlink(),name
 h=hashlib.sha256(p.read_bytes()).hexdigest()
 if before.get(name)==h:continue
 assert (name.startswith('api/image/v1/') and name.endswith('.pb.go')) or (name.startswith(('internal/biz/image/','internal/data/image/','migrations/image/')) and name.endswith('.go')) or name in {'cmd/ani-resource-service/main.go','cmd/ani-resource-service/image_migrate.go'},('unexpected generated change',name)
 changes.append({'path':name,'base_sha256':before.get(name),'sha256':h})
sha=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
manifest={'source_sha':sha,'files':changes}
out=root/'generated-return'/sha;out.mkdir()
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
with tarfile.open(out/'files.tar','w') as tar:
 for item in changes:tar.add(item['path'],arcname=item['path'],recursive=False)
print(json.dumps(manifest,indent=2))
PY
else
 make generate TOOLS_DIR="$root/cache/tools"
 test -z "$(git status --porcelain)"
 bash -n scripts/image-integration scripts/image-tenant-mutations
 go test -count=1 -race ./internal/biz/image ./internal/data/image ./api/image/v1
 ./scripts/image-integration -v -race
 SQLC="$root/cache/tools/sqlc" ./scripts/image-integration --mutations
 make verify TOOLS_DIR="$root/cache/tools"
 git diff a4ca2a0 --exit-code -- ':(glob)migrations/*.sql' api/network README.md AGENTS.md internal/data/network/sqlcgen
fi
