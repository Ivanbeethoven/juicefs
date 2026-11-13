#!/usr/bin/env bash
set -euo pipefail
out=/mnt/c/Users/HXY/.cache/juicefs-txn-opt-20261113
export PATH="/home/hxy/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.linux-amd64/bin:$PATH"
export GOMAXPROCS=4 GOTOOLCHAIN=local GOFLAGS=-p=4
cd /mnt/c/Users/HXY/.cache/juicefs-lazy-fork-20261113/source
git diff --exit-code
git rev-parse HEAD > "$out/after-commit.txt"
make -B juicefs.lite > "$out/build-after.log" 2>&1
cp juicefs.lite "$out/juicefs-after"
sha256sum "$out/juicefs-after" > "$out/after.sha256"
python3 -m py_compile hack/lazy_clone_txn_report.py
