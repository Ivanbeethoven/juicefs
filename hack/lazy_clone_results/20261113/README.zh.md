# JuiceFS lazy fork 本地验证

已实现父层只读、子层独立挂载的共享元数据 fork。创建子层时不遍历或复制 inode、目录项、xattr、chunk 索引。普通 path clone 入口保持原有行为。

## 代码和运行条件

- 分支：`codex/lazy-workspace-fork-20261113`；提交：`7aaafbe3c40b1b377accffd00755a7259f4f44e9`。
- JuiceFS 基线：`f086a159e2a4649999d3b611b7f0fe8bd586ea83`；参考 BrewFS：`03c8cc55d61473533c00a8afe13b4ef1fcd9b725`。
- Linux/WSL Ubuntu 24.04；Go 1.27.1；GOMAXPROCS=4；Redis 7.2.16，本地专用容器，1 CPU / 256 MiB。没有使用云机或付费服务。
- `make juicefs.lite`，正常优化，`-gcflags=""`。二进制 SHA-256：`644c5a86ba1e7eff9efced22fa65138b881fdbe7eb9fd2e1e77d46462d01f9a7`。
- 功能测试对象后端为本地 `file://`；未使用 SQLite。只支持新建 `forkredis://` 格式，不是已有 `redis://` 卷的直接升级。

## fork 成本

每个规模 3 对，顺序 lazy→recursive、recursive→lazy、lazy→recursive。空文件、相同已封存初态，两种算法均使用本分支的 forkredis 引擎。**下面的递归耗时不是原版 Redis 引擎的耗时**。计时从调用元数据 API 到返回，不含导入、封存、挂载、FUSE 或对象 I/O。

| 文件数 | fork 中位数 ms | fork 最小–最大 ms | 递归 clone 中位数 ms | 子层逻辑键数：fork / 递归 |
|---:|---:|---:|---:|---:|
| 100 | 1.643 | 1.610–10.155 | 236.0 | 0 / 209 |
| 1,000 | 1.724 | 1.716–1.966 | 3004.2 | 0 / 2,011 |
| 10,000 | 2.014 | 1.773–2.081 | 34724.2 | 0 / 20,011 |

子层键数在验证枚举之前读取；递归组包含会话、计数器等键。fork 的分支头单独保存，不计入表中逻辑键数。每次 fork 均实测 HSET=1、HINCRBY=1、ZADD=0、ZRANGEBYLEX=0、ZREVRANGEBYSCORE=0（Redis commandstats，包含 Lua 内部命令）。所有结果验证文件数量一致。

另有两次实际 CLI fork，含进程启动，耗时分别为 10.12、10.11 ms；各只新增一个分支头。随后实际挂载两个子层并执行读写，已完成新进程重新挂载验证。

## 已通过

- 9 项分层元数据测试（含子测试），启用 Go race detector；包括只读父层、子层隔离、事务冲突、旧写者拒绝、版本回收、过期读写事务拒绝、空值与墓碑区分。
- 复用 8 组现有 POSIX 辅助用例，包括并发写入、批量 clone、权限、锁、xattr 和 copy_file_range。
- 31 项原版 Redis/通用回归测试，包括 TestRedisClient 和 7 项 Redis BatchClone 测试；在专用容器内运行，未使用宿主的 6379 数据库。
- CLI 参数测试、`go vet ./pkg/meta ./cmd`、Go 格式检查和正常优化构建。
- 真实 FUSE：只读 lower、两子层隔离、覆盖写、截断和空洞、删除、重命名、硬链接、软链接、xattr、重新挂载，以及 gc/destroy 拒绝检查。

原版 Redis 回归使用 `CGO_ENABLED=0 -tags nogspt,nosqlite` 的测试二进制，适配 Redis Alpine 容器。没有执行完整跨平台/数据库/对象存储 CI 矩阵，也没有运行完整的 MinIO CLI 测试套件。

## 本轮发现和边界

初版保留每次修改的完整版本，重复写入测试触发 Redis 256 MiB 容器 OOM。现在读事务登记版本并有 60 秒期限，提交回收不再需要的旧版本；同一并发写测试已通过，原失败日志保留在本地。测试接入时还修正了 xattr 挂载参数、会话初始化和动态端口刷新。

共享层与对象 GC 尚未实现：对象保留，禁止普通 gc/destroy、自动删除/压缩、dump/load 和初始化后的 format/config 更改。目录墓碑和索引仍保留。运行期的层查询与事务登记成本尚未优化；10,000 文件的递归组还发生了大批次事务重试，所以不据此报告相对原版 JuiceFS 的加速比。

未验证：Redis 持久化故障恢复、HA/Cluster、跨机器对象存储、大规模长期运行及共享对象回收。

## 复现与原始记录

- [使用说明](../../../docs/zh_cn/guide/lazy_clone.md)
- [一键验证](../../lazy_clone_check.sh)、[实际 FUSE 验证](../../lazy_clone_smoke.py)
- [逐次 JSON](scaling-01.json)、[逐次 CSV](scaling-rows.csv)、[汇总](summary.json)、[FUSE 结果和完整命令](fuse-result.json)
- [race 日志](meta-race.txt)、[补充 race 日志](extra-race.txt)、[原版 Redis 回归](upstream-redis.txt)、[CLI 测试](cli-tests.txt)、[构建命令](build.txt)

本目录保留测量原值，测试时间和源码提交不随上游同步改写。`summary.json` 的源码提交指向实际测量版本；日志中的本机路径是当时的运行记录。

Linux 本机复现（Go、Python 3、FUSE、专用 Redis）：

```bash
JFS_FORK_REDIS_TEST_ADDR=127.0.0.1:6379 JFS_FORK_BENCH=1 \
  bash hack/lazy_clone_check.sh /tmp/juicefs-lazy-fork-results
```

原版 Redis 回归运行在专用 Redis 容器内，避免测试硬编码的 6379/10 数据库碰到其他数据。容器名称自行指定，测试命令如下；不运行 SQLite。

```bash
CGO_ENABLED=0 go test -tags nogspt,nosqlite -c -o /tmp/meta-static.test ./pkg/meta
docker cp /tmp/meta-static.test "$REDIS_CONTAINER":/tmp/meta-static.test
docker exec -e GOMAXPROCS=4 "$REDIS_CONTAINER" /tmp/meta-static.test \
  -test.run '^(TestRedisClient|TestRedisBatchClone.*|TestExpireTime|TestMergeFacl|TestMergeAttrReplayMode|TestLog.*|TestParseChangeEntry.*|TestChangeEntry.*|TestRemoveSecret|TestEncrypt|TestFormat_Update_KeyConflict|TestOlderThan|TestParseRedis.*|Test_injectPasswordIntoURI|Test_setPasswordFromEnv|Test_readPasswordFromFile|TestSymlinkCache|TestTxBatchLock)$' \
  -test.v -test.timeout 6m
```

