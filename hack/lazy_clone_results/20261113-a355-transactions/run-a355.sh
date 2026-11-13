#!/usr/bin/env bash
set -euo pipefail
root=/srv/juicefs-txn-opt-20261113
exec 9>"$root/bench.lock"
flock -n 9
trap 'status=$?; printf "%s\n" "$status" > "$root/batch.exit"' EXIT
cd "$root"
sha256sum -c <<'HASHES'
ba6ec8a5c48693144971b8d2e3a7593c2c54a8bd4a8f4725c511a0295ba0f151  bin/juicefs-before
28149916f1406395cff4a6b063def26936fea7d5f66720944627b17060a5aeb3  bin/juicefs-after
f732f71bdf9b8032a9f9fc1266ec3d928d40bdac809386b2893cb4b827d546ce  bin/juicefs-uninstrumented
HASHES
chmod +x bin/juicefs-before bin/juicefs-after
for name in calibration-02 quick-01 full-01 fuse-validation; do test ! -e "$root/$name"; done
python3 scripts/lazy_clone_read_bench.py \
  --binary "$root/bin/juicefs-uninstrumented" --source-commit c8a4035a17d79ebdffe2876f85b1ce66c6ee5430 \
  --compare-binary "$root/bin/juicefs-before" --compare-commit 943e569cfb838f93ae64cc2df4b4d86bd78400a7 \
  --out "$root/calibration-02" --profile default --fixture small128 --pairs 3
python3 scripts/lazy_clone_txn_report.py "$root/calibration-02" > "$root/calibration-report.log"
python3 scripts/lazy_clone_read_bench.py \
  --binary "$root/bin/juicefs-before" --source-commit 943e569cfb838f93ae64cc2df4b4d86bd78400a7 \
  --compare-binary "$root/bin/juicefs-after" --compare-commit 4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00 \
  --out "$root/quick-01" --quick --pairs 1
python3 scripts/lazy_clone_txn_report.py "$root/quick-01" > "$root/quick-report.log"
python3 scripts/lazy_clone_read_bench.py \
  --binary "$root/bin/juicefs-before" --source-commit 943e569cfb838f93ae64cc2df4b4d86bd78400a7 \
  --compare-binary "$root/bin/juicefs-after" --compare-commit 4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00 \
  --out "$root/full-01" --pairs 3
python3 scripts/lazy_clone_txn_report.py "$root/full-01" > "$root/full-report.log"
python3 scripts/postflight.py > "$root/postflight.log"
printf 'all tests complete\n'
