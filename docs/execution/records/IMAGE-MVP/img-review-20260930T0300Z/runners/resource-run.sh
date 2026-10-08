#!/usr/bin/env bash
set -Eeuo pipefail
sha=$1 mode=$2 label=$3
[[ $sha =~ ^[0-9a-f]{40}$ && $label =~ ^[a-z0-9-]+$ ]]
root="$HOME/ani-image-mvp-runs/img-review-20260930T0300Z"
prior="$HOME/ani-image-mvp-runs/img-20260929T1718Z"
source_dir="$root/repo/resource/$sha"
evidence="$root/evidence/$label-$sha"
mkdir "$evidence"
exec > >(tee "$evidence/output.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "exit=$rc"; echo "$rc" > "$evidence/exit"' EXIT
date -u +%FT%TZ
hostname
if [[ ! -d $source_dir ]]; then
 git clone --no-checkout --shared "$prior/repo/resource/8f317deef04d034e7b7e0459aacb25a70f01360c" "$source_dir"
 git -C "$source_dir" fetch "$root/inputs/resource.bundle" HEAD
 git -C "$source_dir" checkout --detach "$sha"
fi
cd "$source_dir"
pwd
test "$(git rev-parse HEAD)" = "$sha"
test -z "$(git status --porcelain)"
export TMPDIR="$HOME/.ir-0930" GOCACHE="$prior/cache/go-build" GOMODCACHE="$prior/cache/go-mod" GOPATH="$prior/cache/gopath" GOMAXPROCS=2 GOFLAGS=-p=2 GOMEMLIMIT=1024MiB GOTOOLCHAIN=local GOWORK=off TZ=UTC
export IMAGE_REVIEW_ROOT="$root" IMAGE_REVIEW_EVIDENCE="$evidence"
case "$mode" in
 generate)
 python3 - <<'PY'
import hashlib,json,os,subprocess
from pathlib import Path
files=subprocess.check_output(['git','ls-files','-z']).decode().split('\0')
manifest={f:hashlib.sha256(Path(f).read_bytes()).hexdigest() for f in files if f and not Path(f).is_symlink()}
(Path(os.environ['IMAGE_REVIEW_EVIDENCE'])/'before.json').write_text(json.dumps(manifest))
PY
 make generate TOOLS_DIR="$prior/cache/tools"
 python3 - <<'PY'
import hashlib,json,os,subprocess,tarfile
from pathlib import Path
root=Path(os.environ['IMAGE_REVIEW_ROOT']);ev=Path(os.environ['IMAGE_REVIEW_EVIDENCE']);before=json.loads((ev/'before.json').read_text())
files=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z']).decode().split('\0')
changes=[]
for name in files:
 if not name:continue
 p=Path(name);assert not p.is_symlink(),name
 h=hashlib.sha256(p.read_bytes()).hexdigest()
 if before.get(name)==h:continue
 assert name.endswith('.go') and name.startswith(('internal/biz/image/','internal/data/image/','internal/service/image/','internal/server/','api/image/')),('unexpected generation',name)
 changes.append({'path':name,'base_sha256':before.get(name),'sha256':h})
sha=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
out=root/'generated-return'/sha;out.mkdir()
(out/'manifest.json').write_text(json.dumps({'source_sha':sha,'files':changes},indent=2)+'\n')
with tarfile.open(out/'files.tar','w') as tar:
 for item in changes:tar.add(item['path'],arcname=item['path'],recursive=False)
print(json.dumps({'source_sha':sha,'changes':changes},indent=2))
PY
 ;;
 r1) ./scripts/image-integration -v -run 'TestProjectRejectedCreateSameKeyRetry|TestProjectUncertain|TestLifecycleProjectOwnershipRecovery|TestLifecycleResumeAtPersistenceAndProviderBoundaries' ;;
 r2) ./scripts/image-integration -v -run 'TestSpaceLock|TestSpaceWriteLock|TestLifecycleTwoProcessesSameCommands|TestLifecycleConcurrentCredentialCommand' ;;
 r3) ./scripts/image-integration -v -run 'TestDisablePublisherContract' ;;
 helper|helper-race)
 extra=()
 if [[ $mode == helper-race ]]; then extra=(-race); fi
 go test -mod=readonly -tags=imageintegration "${extra[@]}" -c -o "$root/state/resource-contract-$sha.test" ./internal/data/image
 sha256sum "$root/state/resource-contract-$sha.test" ;;
 image) ./scripts/image-integration -v -race ;;
 mutations) SQLC="$prior/cache/tools/sqlc" ./scripts/image-integration --mutations ;;
 verify) make verify TOOLS_DIR="$prior/cache/tools" ;;
 *) echo 'unsupported stage' >&2; exit 2 ;;
esac
