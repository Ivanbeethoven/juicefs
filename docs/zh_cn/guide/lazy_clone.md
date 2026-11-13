# 共享元数据的工作区 fork（实验功能）

`clone --lazy` 从一个**已封存、只读的父工作区**创建可写子工作区。子工作区独立挂载；父层保持只读。创建时只保存父层引用，不遍历文件，也不复制 inode、目录项、扩展属性或文件块索引。

这个实现采用新的 `forkredis://` 元数据格式，复用 JuiceFS 的 KV 元数据操作、FUSE 和数据块读写。普通 `redis://` 卷和原来的 `juicefs clone SRC DST` 不变。已有 Redis 卷不能直接换 URL 使用本功能，需要先创建新卷、导入文件。

每个客户端默认使用 16 MiB 的元数据读缓存。URL 加 `&fork_cache_bytes=0` 可关闭，或指定字节数（最多 1 GiB）。缓存键包含层、事务版本以及具体键或目录范围，支持记录不存在、空目录范围和删除标记；不会按时间猜测元数据是否过期。每次事务仍向 Redis 固定版本，结束时检查读快照有效期，写提交仍检查版本、封存状态和有效期。失败或过期事务读到的结果不会进入共享缓存。其他客户端提交后版本改变，旧缓存不会用于新版本；本事务尚未提交的修改始终优先。

缓存保存的是元数据结果，容量按键、值及条目开销计费并按 LRU 淘汰；每个在途事务另有至多同等容量的待确认结果。记录读取以最多 128 个键为一批，将各层请求一起发送；目录扫描也按批取值。持久化格式未改变，可与原分支客户端共享已有卷。指标 `forkredis_cache_hits_total`、`forkredis_cache_misses_total`、`forkredis_read_batches_total` 和 `forkredis_cache_bytes` 可用于检查命中和内存；批次数不包含事务校验请求。

## 使用

需要本分支编译的 JuiceFS、Redis 和可用的对象存储。下面用本地文件目录存储数据块，便于复现。

```bash
make juicefs.lite
JFS=./juicefs.lite
BASE='forkredis://127.0.0.1:6379/0?volume=demo&branch=main'
mkdir -p /tmp/jfs-base /tmp/jfs-child
$JFS format --storage file --bucket /tmp/jfs-objects --trash-days 0 "$BASE" demo
$JFS mount -d --backup-meta 0 --enable-xattr "$BASE" /tmp/jfs-base
# 把初始文件写入 /tmp/jfs-base，然后正常卸载，等待写入排空和会话退出。
$JFS umount /tmp/jfs-base
$JFS clone --seal "$BASE"
$JFS clone --lazy "$BASE" child1
CHILD='forkredis://127.0.0.1:6379/0?volume=demo&branch=child1'
$JFS mount -d --backup-meta 0 --enable-xattr "$CHILD" /tmp/jfs-child
# 此后可以只读挂载 BASE；修改只能发生在 CHILD。
```

封存不可撤销。有残留写会话时会拒绝封存，包括过期会话；必须先停止对应进程并清理会话。对子工作区正常卸载、封存后，还可继续 fork。最多 64 层，包含最初的工作区。Redis 密码可通过 `META_PASSWORD` 提供，命令输出隐藏密码。

## 元数据和数据如何共享

- 每层有分支头、键索引和键值记录。子层优先，未修改的键从只读父层读取。删除使用墓碑，目录枚举合并各层并过滤墓碑。
- 写操作只更新相关键。修改一个文件不会复制整棵目录树；部分写入仍走 JuiceFS 原来的 slice 机制。
- slice ID 由整个卷统一分配，避免两个子层写出同名对象。事务失败可以留下未使用 ID，不能重复使用。
- 提交在 Redis Lua 中校验本层版本和封存状态，再原子写入。旧客户端、已经开始但尚未提交的写事务也不能修改已封存父层。
- 读事务登记所读版本，提交时回收不再被读事务需要的旧版本。事务最多运行 60 秒；超过期限的读或写返回错误，不能用已过期的快照成功提交。没有活动旧读者时，反复写同一个键通常只保留最后两个版本。
- fork 新增一条子分支头，并更新父层的子分支计数。开销随层数变化，不随文件数变化；没有后台补做全量元数据复制。

## 当前边界

- 支持 Redis 单节点；尚未验证 Redis HA、集群故障和跨机恢复。Redis 持久性由其 AOF/RDB 配置决定，本实现不替代数据库持久化。
- **尚未实现共享层和对象回收**。所有对象保留，禁用自动删除与压缩；拒绝普通 `gc`、`destroy`、元数据 reset、dump/load、changelog，以及初始化后的 format/config 更改。目录墓碑和索引仍会保留。不能用于需要持续回收空间的生产卷。
- 扫描目前会读取对应前缀的键集合，普通元数据操作多了层查询及事务登记。当前验证的是 fork 不复制元数据；没有声称运行期吞吐优于原版 JuiceFS。
- 原生递归 clone 与本功能语义不同：前者在卷内复制一棵目录的元数据，后者共享整个封存卷并得到独立挂载点。

## 可复用验证

[2026-11-13 本地结果及原始记录](../../../hack/lazy_clone_results/20261113/README.zh.md)。

使用专用 Redis，避免和其他性能任务混跑。需要 Linux、Go、Python 3、`/dev/fuse` 和 `fusermount3`。脚本只使用随机名称的测试卷，不 flush Redis。

```bash
JFS_FORK_REDIS_TEST_ADDR=127.0.0.1:6379 \
  bash hack/lazy_clone_check.sh /tmp/jfs-fork-check-01
```

输出保留源码提交与差异、二进制 SHA、测试日志、实际挂载命令、逐次 fork 耗时和 Redis 命令统计。FUSE 测试覆盖两个子层隔离、只读父层、写入、截断及空洞、删除、重命名、硬链接、软链接、扩展属性和新进程重新挂载。

加入 `JFS_FORK_BENCH=1` 会测 100、1,000、10,000 个空文件，每种方法 3 次，交换执行顺序。对比的是**同一个 forkredis 引擎上的原生递归 clone 与 lazy fork 的元数据 API**，不包含导入、封存、挂载或 FUSE 时间，不能当作原版 Redis JuiceFS 的绝对性能结果。

```bash
JFS_FORK_REDIS_TEST_ADDR=127.0.0.1:6379 JFS_FORK_BENCH=1 \
  bash hack/lazy_clone_check.sh /tmp/jfs-fork-check-02
```

单独重跑实际挂载验证：

```bash
python3 hack/lazy_clone_smoke.py --binary ./juicefs.lite \
  --redis 127.0.0.1:6379 --out /tmp/jfs-fork-smoke-01
```

所有输出目录必须是新的，失败记录不会被覆盖。脚本卸载自己的测试挂载点，但保留测试元数据和对象；一次性 Redis 容器的清理由创建者执行。
