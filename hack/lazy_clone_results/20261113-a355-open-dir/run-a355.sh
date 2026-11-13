#!/usr/bin/env bash
set -euo pipefail
root=/srv/juicefs-open-dir-20261113
exec 9>"$root/bench.lock"
flock -n 9
trap 'rc=$?; printf "%s\n" "$rc" > "$root/batch.exit"' EXIT
cd "$root"
sha256sum -c <<'HASHES'
28149916f1406395cff4a6b063def26936fea7d5f66720944627b17060a5aeb3  bin/juicefs-before
39402a00dc462b0498ab90e0737f1c1674f895c8eed4776bc96ca61cd001abb9  bin/juicefs-after
HASHES
chmod +x bin/juicefs-before bin/juicefs-after
python3 scripts/postflight.py > postflight.log
common=(--binary "$root/bin/juicefs-before" --source-commit 4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00
        --compare-binary "$root/bin/juicefs-after" --compare-commit 8aeb68f21be128d6fb62a1dc4aa6ca7cffef0f5b --pairs 3 --profile default)
python3 scripts/lazy_clone_read_bench.py "${common[@]}" --out "$root/small-01"
python3 scripts/lazy_clone_txn_report.py "$root/small-01" > small-report.log
python3 scripts/lazy_clone_read_bench.py "${common[@]}" --fixture many10000 --metadata-cache-bytes 16777216 --out "$root/large16-01"
python3 scripts/lazy_clone_txn_report.py "$root/large16-01" > large16-report.log
python3 scripts/lazy_clone_read_bench.py "${common[@]}" --fixture many10000 --metadata-cache-bytes 67108864 --out "$root/large64-01"
python3 scripts/lazy_clone_txn_report.py "$root/large64-01" > large64-report.log
echo 'all runs complete'
