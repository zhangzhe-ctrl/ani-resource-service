#!/usr/bin/env bash
set -Eeuo pipefail
sha=$1 mode=$2 label=$3 resource_sha=${4:-}
[[ $sha =~ ^[0-9a-f]{40}$ && $label =~ ^[a-z0-9-]+$ ]]
root="$HOME/ani-image-mvp-runs/img-review-20260930T0300Z"
prior="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/governance/$sha"
evidence="$root/evidence/$label-$sha"
mkdir -p "$(dirname "$source_dir")"
mkdir "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
if [[ ! -d $source_dir ]]; then
 git clone --no-checkout --shared "$prior/repo/governance/d64d6ee478801795afadb4573ba25f8c2de6b7fc-serial" "$source_dir"
 git -C "$source_dir" fetch "$root/inputs/governance.bundle" HEAD
 git -C "$source_dir" checkout --detach "$sha"
fi
cd "$source_dir"
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export TMPDIR="$HOME/.ir-0930" GOCACHE="$prior/cache/go-build" GOMODCACHE="$prior/cache/go-mod" GOPATH="$prior/cache/gopath" GOMAXPROCS=1 GOFLAGS='-p=1 -trimpath' GOMEMLIMIT=768MiB GOGC=50 GOTOOLCHAIN=local GOWORK=off TZ=UTC GOPROXY=https://proxy.golang.org,direct GOSUMDB=sum.golang.org BUF="$prior/cache/tools/buf"
export IMAGE_REVIEW_ROOT="$root" IMAGE_REVIEW_EVIDENCE="$evidence"
if [[ -n $resource_sha ]]; then
 [[ $resource_sha =~ ^[0-9a-f]{40}$ ]]
 export IMAGE_RESOURCE_CONTRACT_BINARY="$root/state/resource-contract-$resource_sha.test"
 test -x "$IMAGE_RESOURCE_CONTRACT_BINARY"
 printf 'Resource server source_sha=%s\n' "$resource_sha"
 sha256sum "$IMAGE_RESOURCE_CONTRACT_BINARY"
fi
case "$mode" in
 generate)
 python3 - <<'PY'
import hashlib,json,os,subprocess
from pathlib import Path
files=subprocess.check_output(['git','ls-files','-z']).decode().split('\0')
manifest={f:hashlib.sha256(Path(f).read_bytes()).hexdigest() for f in files if f and not Path(f).is_symlink()}
(Path(os.environ['IMAGE_REVIEW_EVIDENCE'])/'before.json').write_text(json.dumps(manifest))
PY
 scripts/generate-image-slice.sh
 git diff d64d6ee478801795afadb4573ba25f8c2de6b7fc --name-only -z -- '*.go' | xargs -0 --no-run-if-empty gofmt -w
 python3 - <<'PY'
import hashlib,json,os,subprocess,tarfile
from pathlib import Path
root=Path(os.environ['IMAGE_REVIEW_ROOT']);ev=Path(os.environ['IMAGE_REVIEW_EVIDENCE']);before=json.loads((ev/'before.json').read_text())
files=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z']).decode().split('\0');changes=[]
for name in files:
 if not name:continue
 p=Path(name)
 if p.is_symlink():continue
 h=hashlib.sha256(p.read_bytes()).hexdigest()
 if before.get(name)==h:continue
 assert name in ['app/admin/service/internal/service/image_views.go','app/admin/service/internal/service/image_resource_contract_test.go','app/admin/service/internal/service/image_joint_http_test.go'],('unexpected generated change',name)
 changes.append({'path':name,'base_sha256':before.get(name),'sha256':h})
sha=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();out=root/'generated-return'/('gov-'+sha);out.mkdir()
(out/'manifest.json').write_text(json.dumps({'source_sha':sha,'files':changes},indent=2)+'\n')
with tarfile.open(out/'files.tar','w') as tar:
 for item in changes:tar.add(item['path'],arcname=item['path'],recursive=False)
print(json.dumps({'source_sha':sha,'changes':changes},indent=2))
PY
 ;;
 joint) scripts/image-joint-integration ;;
 build) go build -mod=readonly ./app/admin/service/cmd/server ./app/admin/service/cmd/admin ;;
 contracts) go mod tidy -diff; go test -mod=readonly -count=1 ./tests/imagecontract ./sql/bootstrap ./pkg/middleware/auth ./pkg/middleware/logging ./pkg/constants ./pkg/localdeps/kratos-bootstrap/rpc ;;
 regressions) scripts/image-joint-integration --regressions ;;
 race)
 export GOFLAGS='-p=1 -trimpath -gcflags=go-wind-admin/app/admin/service/internal/data/ent=-l'
 printf 'race: only generated Ent inlining disabled; all race instrumentation retained\n'
 go test -mod=readonly -count=1 -race ./pkg/middleware/auth ./app/admin/service/internal/data ./app/admin/service/internal/service -run Image
 scripts/image-joint-integration -race ;;
 *) exit 2 ;;
esac
