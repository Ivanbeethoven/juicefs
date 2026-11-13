# Lazy workspace fork (experimental)

`juicefs clone --lazy META-URL CHILD` creates a separately mounted, writable
workspace referencing a sealed, read-only lower. It creates a branch header and
updates the parent's reference count without enumerating or copying inodes,
dentries, xattrs, or chunk metadata. The source must be cleanly unmounted and
sealed with `juicefs clone --seal META-URL` first. Sealing is permanent.

This opt-in engine uses `forkredis://host:port/db?volume=NAME&branch=main`.

Each client defaults to a 16 MiB versioned metadata read cache. Add
`&fork_cache_bytes=0` to disable it, or specify a byte budget up to 1 GiB.
Cache keys include the layer, pinned revision and exact key or index range;
absent records, empty ranges and tombstones are cached distinctly. Transactions
still pin their revision in Redis and validate expiry on completion. Writes
retain revision, expiry and seal checks. Only successfully validated reads enter
the shared cache, and in-transaction writes always take precedence. A commit by
another client changes the revision, preventing reuse of old upper-layer results.

The byte-accounted LRU includes key/value and entry overhead. Each active
transaction may additionally retain up to the same budget of pending results.
Reads pipeline at most 128 keys across their layers per batch; directory scans
also fetch values in batches. The persisted format is unchanged and remains
compatible with earlier clients on this branch. Metrics expose cache hits,
misses, accounted bytes and read pipeline executions under `forkredis_cache_*`
and `forkredis_read_batches_total`; pipeline counts exclude transaction validation.
It reuses JuiceFS KV metadata operations, FUSE and chunk storage. It is a new
format, **not an in-place upgrade of an existing Redis volume**. Ordinary
`redis://` volumes and path-based `clone SRC DST` retain their behavior.

```bash
make juicefs.lite
BASE='forkredis://127.0.0.1:6379/0?volume=demo&branch=main'
mkdir -p /tmp/jfs-base /tmp/jfs-child
./juicefs.lite format --storage file --bucket /tmp/jfs-objects --trash-days 0 "$BASE" demo
./juicefs.lite mount -d --backup-meta 0 "$BASE" /tmp/jfs-base
# Populate the source, then unmount and wait for its session to close.
./juicefs.lite umount /tmp/jfs-base
./juicefs.lite clone --seal "$BASE"
./juicefs.lite clone --lazy "$BASE" child1
./juicefs.lite mount -d --backup-meta 0 \
  'forkredis://127.0.0.1:6379/0?volume=demo&branch=child1' /tmp/jfs-child
```

Upper records override inherited records; tombstones hide deleted entries.
Writes change only affected keys. Slice IDs are allocated across the volume to
prevent object-name collisions between siblings. Commit scripts check the head
revision and sealed flag, including writes from clients opened before sealing.
Read transactions pin record versions for at most 60 seconds. Commits prune
versions older than those still needed; expired transactions fail. No callback
is silently rerun by the driver. Ancestry is limited to 64 layers.

Shared-layer/object reclamation is **not implemented**. All objects are retained;
automatic deletion/compaction, ordinary `gc`/`destroy`, reset, dump/load,
changelog, and post-initialization format/config changes are disabled. Directory
indices and tombstones remain. This is an experimental implementation, not a
production volume with bounded storage usage. Only standalone Redis is supported;
HA/failover and cross-machine recovery have not been verified. Redis AOF/RDB
configuration determines database persistence.

Run local tests against a dedicated Redis (Linux, Go, Python 3 and FUSE required):

[Local results and raw evidence (2026-11-13, Chinese)](../../../hack/lazy_clone_results/20261113/README.zh.md).

```bash
JFS_FORK_REDIS_TEST_ADDR=127.0.0.1:6379 \
  bash hack/lazy_clone_check.sh /tmp/jfs-fork-check-01
```

The new output directory holds source/binary hashes, logs, commands and raw FUSE
results. Tests cover lower protection, sibling isolation, writes/truncation,
tombstones, rename, links, xattrs and remount. Set `JFS_FORK_BENCH=1` to include
three alternating runs each at 100/1,000/10,000 empty files. That comparison uses
recursive clone and lazy fork **on this same experimental metadata engine**;
it excludes import, seal, mount and FUSE cost and is not a measurement of the
upstream Redis driver's performance. Runtime throughput has not been optimized.
Scripts retain evidence and test data, and never flush Redis.

See the [Chinese implementation notes](../../zh_cn/guide/lazy_clone.md) for the
full mechanism and individual test commands.
