#!/usr/bin/env bash
# Local-only verification of the experimental shared-metadata workspace engine.
set -euo pipefail
: "${JFS_FORK_REDIS_TEST_ADDR:?set a dedicated unauthenticated Redis host:port}"
out=${1:?usage: bash hack/lazy_clone_check.sh NEW_OUTPUT_DIRECTORY}
test ! -e "$out"
mkdir -p "$out"
out=$(realpath "$out")
cd "$(dirname "$0")/.."
export GOMAXPROCS=${GOMAXPROCS:-4} GOFLAGS=${GOFLAGS:--p=4}
git rev-parse HEAD >"$out/source-commit.txt"
git status --short >"$out/source-status.txt"
git diff HEAD >"$out/source-diff.patch"
go version >"$out/toolchain.txt"
env -u JFS_FORK_BENCH_OUTPUT go test -race -count=1 -v -timeout=5m ./pkg/meta -run '^TestForkRedis' >"$out/meta-tests.log" 2>&1
go test -count=1 -v -timeout=2m ./cmd -run '^TestCloneWorkspaceArguments$' >"$out/cmd-tests.log" 2>&1
go vet ./pkg/meta ./cmd >"$out/vet.log" 2>&1
make juicefs.lite >"$out/build.log" 2>&1
cp juicefs.lite "$out/juicefs"
sha256sum "$out/juicefs" >"$out/binary.sha256"
python3 hack/lazy_clone_smoke.py --binary "$out/juicefs" \
  --redis "$JFS_FORK_REDIS_TEST_ADDR" --out "$out/fuse" >"$out/fuse.log" 2>&1
if [[ ${JFS_FORK_BENCH:-0} == 1 ]]; then
  JFS_FORK_BENCH_OUTPUT="$out/scaling.json" go test -count=1 -v -timeout=10m \
    ./pkg/meta -run '^TestForkRedisScaling$' >"$out/scaling.log" 2>&1
fi
printf 'Results: %s\n' "$out"
