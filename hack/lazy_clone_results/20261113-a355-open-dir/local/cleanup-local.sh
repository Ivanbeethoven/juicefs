#!/usr/bin/env bash
set -euo pipefail
name=juicefs-open-dir-20261113-redis
test "$(docker inspect -f '{{index .Config.Labels "codex.owner"}}' "$name")" = juicefs-open-dir-20261113
docker stop "$name"
docker rm "$name"
if findmnt -rn -o TARGET | grep -F /home/hxy/.cache/juicefs-open-dir-20261113/fuse/; then
  exit 1
fi
echo 'owned local Redis removed; no test FUSE mounts remain'
