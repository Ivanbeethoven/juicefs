#!/usr/bin/env bash
set -euo pipefail
root=/srv/juicefs-txn-opt-20261113
exec 9>"$root/bench.lock"
flock -n 9
trap 'status=$?; printf "%s\n" "$status" > "$root/default-02.exit"' EXIT
cd "$root"
sha256sum -c <<'HASHES'
ba6ec8a5c48693144971b8d2e3a7593c2c54a8bd4a8f4725c511a0295ba0f151  bin/juicefs-before
28149916f1406395cff4a6b063def26936fea7d5f66720944627b17060a5aeb3  bin/juicefs-after
HASHES
python3 scripts/lazy_clone_read_bench.py \
  --binary "$root/bin/juicefs-before" --source-commit 943e569cfb838f93ae64cc2df4b4d86bd78400a7 \
  --compare-binary "$root/bin/juicefs-after" --compare-commit 4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00 \
  --out "$root/default-02" --profile default --pairs 3
python3 scripts/lazy_clone_txn_report.py "$root/default-02" > "$root/default-02-report.log"
