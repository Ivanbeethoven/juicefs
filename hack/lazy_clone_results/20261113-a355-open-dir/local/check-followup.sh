#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-open-dir-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
gofmt -w pkg/meta/tkv.go pkg/meta/tkv_forkredis.go pkg/meta/tkv_forkredis_test.go
name=juicefs-open-dir-20261113-redis
test "$(docker inspect -f '{{index .Config.Labels "codex.owner"}}' "$name")" = juicefs-open-dir-20261113
docker start "$name" > "$out/redis-restart.txt"
export JFS_FORK_REDIS_TEST_ADDR
JFS_FORK_REDIS_TEST_ADDR=$(docker port "$name" 6379/tcp)
go test -race -count=1 -v -timeout=3m ./pkg/meta -run '^TestForkRedis(CachedReadValidation|DirectoryCachedRead|ReadCacheConsistency|BatchAndCacheLimits)$' > "$out/fork-followup.log" 2>&1
go vet ./pkg/meta > "$out/vet-final.log" 2>&1
python3 -m py_compile hack/lazy_clone_read_bench.py
echo 'followup checks complete'
