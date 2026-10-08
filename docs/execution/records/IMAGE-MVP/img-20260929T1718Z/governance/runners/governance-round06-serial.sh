#!/usr/bin/env bash
set -Eeuo pipefail
sha=$1
mode=$2
[[ $sha =~ ^[0-9a-f]{40}$ ]]
[[ $mode == generate || $mode == verify ]]
root="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/governance/$sha${3:-}"
evidence="$root/evidence/IMG-06/gov-$sha-$mode${3:-}"
mkdir -p "$evidence" "$(dirname "$source_dir")"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'exit 143' TERM
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
export TMPDIR="$HOME/.im-1718" GOCACHE="$root/cache/go-build" GOMODCACHE="$root/cache/go-mod" GOPATH="$root/cache/gopath" GOMAXPROCS=2 GOFLAGS='-p=1 -trimpath' GOMEMLIMIT=768MiB GOGC=50 GOTOOLCHAIN=local GOWORK=off TZ=UTC GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org BUF="$root/cache/tools/buf"
export IMAGE_ROUND_EVIDENCE="$evidence" IMAGE_ROUND_ROOT="$root"
python3 - <<'PY'
import hashlib,json,os,subprocess
from pathlib import Path
files=subprocess.check_output(['git','ls-files','-z']).decode().split('\0')
manifest={f:hashlib.sha256(Path(f).read_bytes()).hexdigest() for f in files if f and not Path(f).is_symlink()}
(Path(os.environ['IMAGE_ROUND_EVIDENCE'])/'before.json').write_text(json.dumps(manifest))
PY
if [[ $mode == generate ]]; then
 go get github.com/zhangzhe-ctrl/ani-resource-service@71aa986078dfb64388d783a7b2de01b7a94b0027
fi
bash -n scripts/generate-image-slice.sh
scripts/generate-image-slice.sh
make gow
go list -export -deps ./app/admin/service/internal/data/ent/schema >/dev/null
make -C app/admin/service ent
if [[ $mode == generate ]]; then
 go mod tidy
 git diff f535b64d7da25061f5742befdcdfe52c5aef1615 --name-only -z -- '*.go' | xargs -0 --no-run-if-empty gofmt -w
 python3 - <<'PY'
import hashlib,json,os,subprocess,tarfile
from pathlib import Path
root=Path(os.environ['IMAGE_ROUND_ROOT']);ev=Path(os.environ['IMAGE_ROUND_EVIDENCE']);before=json.loads((ev/'before.json').read_text())
allowed=set((root/'inputs/gov06-whitelist.txt').read_text().splitlines())
for name in before:assert Path(name).exists(),('deleted source',name)
files=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z']).decode().split('\0')
changes=[]
for name in files:
 if not name:continue
 p=Path(name)
 if p.is_symlink():continue
 h=hashlib.sha256(p.read_bytes()).hexdigest()
 if before.get(name)==h:continue
 assert name in allowed,('unexpected generated change',name)
 changes.append({'path':name,'base_sha256':before.get(name),'sha256':h})
sha=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();manifest={'source_sha':sha,'resource_contract_sha':'71aa986078dfb64388d783a7b2de01b7a94b0027','files':changes}
out=root/'generated-return'/('gov-'+sha);out.mkdir()
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
with tarfile.open(out/'files.tar','w') as tar:
 for item in changes:tar.add(item['path'],arcname=item['path'],recursive=False)
print(json.dumps(manifest,indent=2))
PY
else
 test -z "$(git status --porcelain)"
 go mod tidy -diff
 go test -mod=readonly -count=1 ./tests/imagecontract ./sql/bootstrap ./pkg/middleware/auth ./pkg/middleware/logging ./pkg/constants ./pkg/localdeps/kratos-bootstrap/rpc
 scripts/image-joint-integration --regressions
 go test -mod=readonly -count=1 -race ./pkg/middleware/auth ./app/admin/service/internal/data ./app/admin/service/internal/service -run Image
 go build -mod=readonly ./app/admin/service/cmd/server ./app/admin/service/cmd/admin
 bash -n scripts/image-joint-integration
 scripts/image-joint-integration -race
 git diff --check
fi
