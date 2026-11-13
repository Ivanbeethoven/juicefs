#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-txn-opt-20261113
repo=/mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
name=juicefs-txn-opt-20261113-redis
test "$(docker inspect -f '{{index .Config.Labels "codex.owner"}}' "$name")" = juicefs-txn-opt-20261113
docker start "$name" > "$out/smoke-redis-start.txt"
redis_addr=$(docker port "$name" 6379/tcp)
python3 "$repo/hack/lazy_clone_smoke.py" --binary "$out/juicefs-after" --redis "$redis_addr" --out /tmp/juicefs-txn-opt-20261113-fuse > "$out/local-fuse.log" 2>&1
cp /tmp/juicefs-txn-opt-20261113-fuse/result.json "$out/local-fuse-result.json"
printf 'local FUSE smoke complete\n'
