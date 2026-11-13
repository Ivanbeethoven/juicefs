#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-txn-opt-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
git rev-parse HEAD > "$out/before-commit.txt"
make -B juicefs.lite > "$out/build-before.log" 2>&1
cp juicefs.lite "$out/juicefs-before"
sha256sum "$out/juicefs-before" > "$out/before.sha256"
