# forkredis 热缓存事务优化：本地验证

本轮已完成代码修改、分段计时、release 构建和本地正确性验证。**A355 新旧性能对照尚未完成；这里没有新的加速比例。** 旧实验结果保留在原目录。

## 改动

此前，即使键值和目录范围全部命中缓存，每次读仍要执行 `forkBegin` 和 `forkEnd`，向 Redis 登记读版本、确认未过期并释放登记。数据查询为零不代表 Redis 往返为零。

现在对点读取、关闭 open-cache 时的 lookup、无过滤器的目录扫描，先将同一版本下的缓存内容收集到临时变量，再用一次 `HMGET` 原子检查分支版本及封存状态。只有完全命中且版本一致才返回结果。缓存缺项或版本变化时，丢弃临时结果，使用原来的事务。缓存大小仍为 16 MiB。

缓存来自以前校验成功的事务，键含层、版本和查询范围；读取返回独立字节副本。新路径不再读取 Redis 中的旧记录，因此不需要登记其保留期限。结果在最后一次版本检查处确定。版本检查之前不更新 inode 属性缓存、不返回目录项。所有写操作仍经过原来的版本、封存状态、过期检查及原子提交；没有扩大写入权限，也没有延迟写回。任意事务回调不会被自动重跑。

## 验证结果

| 项目 | 结果 |
|---|---|
| forkredis race 测试 | 14 个顶层、含子测试 31 项通过；1 个可选规模测试跳过 |
| 原生 Redis、MemKV、通用元数据 | 37 个顶层、含子测试 265 项通过；2 个不适用项跳过 |
| `go vet ./pkg/meta` | 通过 |
| WSL 实际 FUSE 挂载 | 父层只读、兄弟隔离、写入、截断空洞、删除、重命名、链接、xattr、重新挂载通过 |
| 报表脚本 | 在历史数据副本的 6 项结果上通过，另核对分段及 kind 标签解析；未改历史数据 |
| A355 耗时对照 | 待完成：SSH 在握手阶段断开 |

新增回归覆盖：一次完整缓存读取只做一次版本确认、不创建 Redis 读者登记；收集期间另一客户端提交导致回退；缓存缺项不先发 Redis 查询；取消及超时；只读路径拒绝写入；另一客户端截断、重命名和删除后读到新状态；目录扫描中途缺项后回退不会重复返回条目。已有过期快照、封存拒绝旧写者、墓碑、兄弟隔离和并发写测试继续通过。

按照用户要求没有执行 SQLite 测试。这里不是完整的跨后端 CI、Redis HA 或故障恢复验证。`core-tests.log` 是执行环境中断留下的不完整首轮日志；`core-tests-02.log` 为重跑完整通过的记录。原始日志未覆盖。

## 两个构建

| 构建 | 源码 | 二进制 SHA-256 |
|---|---|---|
| before：只加计时，读路径不变 | `943e569cfb838f93ae64cc2df4b4d86bd78400a7` | `ba6ec8a5c48693144971b8d2e3a7593c2c54a8bd4a8f4725c511a0295ba0f151` |
| after：一次缓存版本确认 | `4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00` | `28149916f1406395cff4a6b063def26936fea7d5f66720944627b17060a5aeb3` |

Go 1.27.1，`make -B juicefs.lite`，正常优化，未设置 `-N`。两版均有计时指标。本机二进制在 `C:/Users/HXY/.cache/juicefs-txn-opt-20261113/juicefs-before` 和 `juicefs-after`。

## A355 续跑

SSH 别名 `idcai-client2`；测试根目录 `/srv/juicefs-txn-opt-20261113`。该目录已有 before 及上一轮未加计时的二进制。连接中断前启动过 `calibrate.sh`，输出目录为 `calibration-01`；**其完成状态和结果尚未取回核验**。先读 `calibration.exit`、`calibration.log` 和 `calibration-01/run.json`，检查进程、挂载及宿主空闲状态，再启动新任务。不要重复运行已经存在的输出目录。

上传 after 及本分支 `hack/lazy_clone*.py`，核对 SHA。一次只运行一组性能任务：

```bash
ROOT=/srv/juicefs-txn-opt-20261113
python3 "$ROOT/scripts/lazy_clone_read_bench.py" \
  --binary "$ROOT/bin/juicefs-before" \
  --source-commit 943e569cfb838f93ae64cc2df4b4d86bd78400a7 \
  --compare-binary "$ROOT/bin/juicefs-after" \
  --compare-commit 4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00 \
  --out "$ROOT/full-01" --pairs 3 \
  --client-cpus 8-11 --redis-cpu 12 --driver-cpu 13
python3 "$ROOT/scripts/lazy_clone_txn_report.py" "$ROOT/full-01"
```

默认共 108 项：三个负载、两档元数据 TTL、三对重复、单层/Overlay/原生三个对照、两个二进制。每对交替版本顺序；两版保持相同 16 MiB 元数据缓存、128 MiB 数据缓存和 FUSE 参数。每项先证明数据已热，计时期间对象 GET/字节为零，FUSE 读取量必须与负载一致。

分段表输出为 `TRANSACTIONS.zh.md`、`txn-summary.json`、`txn-cells.csv`。记录 begin、data_io、end、commit、cached_prepare、cached_validate 等客户端累计时间，以及 Redis 命令 calls/usec。body 包含 data_io，transaction 包含其内部阶段；不能重复相加。Lua 的 commandstats 包含内部命令，命令总数不等于网络往返数。

取回逐次数据和原始 Prometheus/Redis commandstats 后，才能回答阶段占比、实际改善多少及相对原生还慢多少。A355 现有监控和其他挂载不属于本任务，保留；本轮没有启动云机或付费资源。
