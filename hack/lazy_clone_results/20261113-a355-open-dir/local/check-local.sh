#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-open-dir-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
gofmt -w pkg/meta/tkv.go pkg/meta/tkv_forkredis.go pkg/meta/tkv_forkredis_test.go
git diff HEAD > "$out/tested-source.patch"
name=juicefs-open-dir-20261113-redis
test -z "$(docker ps -aq --filter name=^/${name}$)"
docker run --pull never -d --name "$name" --label codex.owner=juicefs-open-dir-20261113 --memory 256m --cpus 1 -p 127.0.0.1::6379 sha256:63218503f6461303e502355b8b0359a1194a190341f954e7fc7e8f92cd382a43 redis-server --save '' --appendonly no > "$out/redis-container.txt"
docker port "$name" 6379/tcp > "$out/redis-address.txt"
export JFS_FORK_REDIS_TEST_ADDR
JFS_FORK_REDIS_TEST_ADDR=$(cat "$out/redis-address.txt")
go test -race -count=1 -v -timeout=5m ./pkg/meta -run '^TestForkRedis' > "$out/fork-race.log" 2>&1
go vet ./pkg/meta > "$out/vet.log" 2>&1
CGO_ENABLED=0 go test -tags nogspt,nosqlite -c -o "$out/meta.test" ./pkg/meta > "$out/core-build.log" 2>&1
docker cp "$out/meta.test" "$name:/tmp/meta.test"
docker exec -e GOMAXPROCS=4 "$name" /tmp/meta.test -test.run '^(TestRedisClient|TestRedisBatchClone.*|TestMemKVClient|TestKV.*|TestExpireTime|TestMergeFacl|TestMergeAttrReplayMode|TestLog.*|TestParseChangeEntry.*|TestChangeEntry.*|TestRemoveSecret|TestEncrypt|TestFormat_Update_KeyConflict|TestOlderThan|TestParseRedis.*|Test_injectPasswordIntoURI|Test_setPasswordFromEnv|Test_readPasswordFromFile|TestSymlinkCache|TestTxBatchLock)$' -test.v -test.timeout 8m > "$out/core-tests.log" 2>&1
echo 'all correctness checks complete'
