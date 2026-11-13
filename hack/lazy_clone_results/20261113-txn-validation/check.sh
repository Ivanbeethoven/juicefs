#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-txn-opt-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
gofmt -w pkg/meta/tkv.go pkg/meta/tkv_forkredis.go pkg/meta/tkv_forkredis_read.go pkg/meta/tkv_forkredis_cache.go pkg/meta/tkv_forkredis_timing.go pkg/meta/tkv_forkredis_test.go
name=juicefs-txn-opt-20261113-redis
test -z "$(docker ps -aq --filter name=^/${name}$)"
docker run --pull never -d --name "$name" --label codex.owner=juicefs-txn-opt-20261113 --memory 256m --cpus 1 -p 127.0.0.1::6379 sha256:63218503f6461303e502355b8b0359a1194a190341f954e7fc7e8f92cd382a43 redis-server --save '' --appendonly no >"$out/redis-container.txt"
docker port "$name" 6379/tcp >"$out/redis-address.txt"
export JFS_FORK_REDIS_TEST_ADDR
JFS_FORK_REDIS_TEST_ADDR=$(cat "$out/redis-address.txt")
go test -race -count=1 -v -timeout=5m ./pkg/meta -run '^TestForkRedis' >"$out/fork-tests-01.log" 2>&1
go vet ./pkg/meta >"$out/vet.log" 2>&1
printf 'checks complete\n'
