#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-open-dir-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
git diff --quiet
git rev-parse HEAD > "$out/source-commit.txt"
make -B juicefs.lite > "$out/build.log" 2>&1
cp juicefs.lite "$out/juicefs-after"
sha256sum "$out/juicefs-after" > "$out/binary.sha256"
name=juicefs-open-dir-20261113-redis
test "$(docker inspect -f '{{index .Config.Labels "codex.owner"}}' "$name")" = juicefs-open-dir-20261113
docker start "$name" > "$out/smoke-redis-start.txt"
address=$(docker port "$name" 6379/tcp)
python3 hack/lazy_clone_smoke.py --binary "$out/juicefs-after" --redis "$address" --out /home/hxy/.cache/juicefs-open-dir-20261113/fuse > "$out/fuse.log" 2>&1
cp /home/hxy/.cache/juicefs-open-dir-20261113/fuse/result.json "$out/fuse-result.json"
echo 'release build and FUSE check complete'
