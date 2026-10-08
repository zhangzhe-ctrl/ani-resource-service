#!/usr/bin/env bash
set -Eeuo pipefail
root="$HOME/ani-image-mvp-runs/img-review-20260930T0300Z"
rsha=fa56b55dbbc892bc40586a8303bff54826764e7e
gsha=e608cba9bf7c5ccfdb3dad41471525ff541c1ad1
exec > >(tee "$root/evidence/regression-supervisor.txt") 2>&1
trap 'rc=$?; date -u +%FT%TZ; echo "supervisor_exit=$rc"; echo "$rc" > "$root/evidence/regression-supervisor.exit"' EXIT
run() {
 local cpu=$1 memory=$2 seconds=$3 runner=$4 sha=$5 mode=$6 label=$7
 shift 7
 printf 'command=systemd-run --user --scope CPUQuota=%s MemoryMax=%s MemorySwapMax=0 timeout=%ss %s %s %s %s %s\n' "$cpu" "$memory" "$seconds" "$runner" "$sha" "$mode" "$label" "$*"
 systemd-run --user --scope -p "CPUQuota=$cpu" -p "MemoryMax=$memory" -p MemorySwapMax=0 timeout "$seconds" bash "$root/state/$runner" "$sha" "$mode" "$label" "$@"
}
run 200% 2300M 1200 resource-run.sh "$rsha" generate final-resource-gen
run 100% 1532M 1200 resource-run.sh "$rsha" r1 final-r1
run 100% 1532M 1200 resource-run.sh "$rsha" r2 final-r2
run 100% 1532M 1200 resource-run.sh "$rsha" r3 final-r3
run 200% 2300M 1200 resource-run.sh "$rsha" verify final-resource-verify
run 100% 1532M 1200 resource-run.sh "$rsha" image final-image-race
run 100% 1532M 1200 resource-run.sh "$rsha" mutations final-image-mutations
run 200% 2300M 1200 resource-run.sh "$rsha" helper-race final-resource-helper
run 200% 2300M 1200 governance-run.sh "$gsha" build final-governance-build
run 200% 2300M 1200 governance-run.sh "$gsha" contracts final-governance-contracts
run 50% 1404M 1200 governance-run.sh "$gsha" regressions final-governance-regressions
run 50% 1404M 1200 governance-run.sh "$gsha" joint final-governance-http "$rsha"
run 50% 1404M 1200 governance-run.sh "$gsha" race final-governance-race "$rsha"
run 100% 1532M 1500 network-run.sh "$rsha" integration final-network-integration
run 100% 1532M 1500 network-run.sh "$rsha" race final-network-race
run 100% 1532M 1200 network-run.sh "$rsha" mutations final-network-mutations
