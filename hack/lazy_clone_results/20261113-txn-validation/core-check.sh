#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-txn-opt-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
name=juicefs-txn-opt-20261113-redis
test "$(docker inspect -f '{{index .Config.Labels "codex.owner"}}' "$name")" = juicefs-txn-opt-20261113
docker start "$name" > "$out/redis-restart.txt"
CGO_ENABLED=0 go test -tags nogspt,nosqlite -c -o "$out/meta.test" ./pkg/meta > "$out/core-build.log" 2>&1
docker cp "$out/meta.test" "$name:/tmp/meta.test"
docker exec -e GOMAXPROCS=4 "$name" /tmp/meta.test -test.run '^(TestRedisClient|TestRedisBatchClone.*|TestMemKVClient|TestKV.*|TestExpireTime|TestMergeFacl|TestMergeAttrReplayMode|TestLog.*|TestParseChangeEntry.*|TestChangeEntry.*|TestRemoveSecret|TestEncrypt|TestFormat_Update_KeyConflict|TestOlderThan|TestParseRedis.*|Test_injectPasswordIntoURI|Test_setPasswordFromEnv|Test_readPasswordFromFile|TestSymlinkCache|TestTxBatchLock)$' -test.v -test.timeout 8m > "$out/core-tests-02.log" 2>&1
printf 'selected core and Redis tests complete\n'
