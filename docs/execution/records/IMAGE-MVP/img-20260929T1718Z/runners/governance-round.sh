#!/usr/bin/env bash
set -Eeuo pipefail
sha=$1
mode=$2
[[ $sha =~ ^[0-9a-f]{40}$ ]]
[[ $mode == generate || $mode == verify ]]
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/governance/$sha"
evidence="$root/evidence/IMG-01/gov-$sha-$mode"
mkdir -p "$evidence" "$(dirname "$source_dir")"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
if [[ ! -d $source_dir ]]; then
 git clone --no-checkout --shared "$root/repo/governance-baseline" "$source_dir"
 git -C "$source_dir" fetch "$root/inputs/governance-current.bundle" HEAD
 git -C "$source_dir" checkout --detach "$sha"
fi
cd "$source_dir"
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export TMPDIR="$root/cache/tmp" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1536MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC BUF="$root/cache/tools/buf"
export IMAGE_ROUND_EVIDENCE="$evidence" IMAGE_ROUND_ROOT="$root"
if [[ $mode == generate ]]; then
 python3 - <<'PY'
import hashlib,json,os,subprocess
from pathlib import Path
files=subprocess.check_output(['git','ls-files','-z']).decode().split('\0')
manifest={f:hashlib.sha256(Path(f).read_bytes()).hexdigest() for f in files if f and not Path(f).is_symlink()}
(Path(os.environ['IMAGE_ROUND_EVIDENCE'])/'before.json').write_text(json.dumps(manifest))
PY
 bash -n scripts/generate-image-slice.sh
 scripts/generate-image-slice.sh
 gofmt -w tests/imagecontract/contract_test.go
 python3 - <<'PY'
import hashlib,json,os,subprocess,tarfile
from pathlib import Path
root=Path(os.environ['IMAGE_ROUND_ROOT']);ev=Path(os.environ['IMAGE_ROUND_EVIDENCE']);before=json.loads((ev/'before.json').read_text())
files=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z']).decode().split('\0')
changes=[]
allowed={'api/gen/go/catalog/service/v1/image.pb.go','api/gen/go/admin/service/v1/i_image.pb.go','api/gen/go/admin/service/v1/i_image_grpc.pb.go','api/gen/go/admin/service/v1/i_image_http.pb.go','tests/imagecontract/contract_test.go'}
for name in files:
 if not name:continue
 p=Path(name)
 if p.is_symlink():continue
 h=hashlib.sha256(p.read_bytes()).hexdigest()
 if before.get(name)==h:continue
 assert name in allowed,('unexpected generated change',name)
 changes.append({'path':name,'base_sha256':before.get(name),'sha256':h})
sha=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
manifest={'source_sha':sha,'files':changes}
out=root/'generated-return'/('gov-'+sha);out.mkdir()
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
with tarfile.open(out/'files.tar','w') as tar:
 for item in changes:tar.add(item['path'],arcname=item['path'],recursive=False)
print(json.dumps(manifest,indent=2))
PY
else
 bash -n scripts/generate-image-slice.sh
 scripts/generate-image-slice.sh
 test -z "$(gofmt -l tests/imagecontract/contract_test.go)"
 test -z "$(git status --porcelain)"
 go test -mod=readonly -count=1 ./tests/imagecontract
 git diff --check
fi
