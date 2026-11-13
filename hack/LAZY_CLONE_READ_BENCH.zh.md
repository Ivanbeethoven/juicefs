# 测 JuiceFS fork 子层的热读开销

[A355 的 54 项实测结果与原始记录](lazy_clone_results/20261113-a355-hot-read/README.zh.md)。

[优化前后 108 项对照、18 项消融及一致性检查](lazy_clone_results/20261113-a355-optimized/README.zh.md)。

[事务往返优化的代码与本地验证、A355 续跑命令](lazy_clone_results/20261113-txn-validation/README.zh.md)。该轮 A355 新旧耗时尚待完成。

## 对照

| 名称 | 元数据引擎 | 挂载方式 | 用途 |
|---|---|---|---|
| lower | forkredis | 封存父层，只读 | 同引擎、单层基准 |
| overlay | forkredis | 同一父层的可写子工作区 | 测多一层的开销；子层没有文件修改 |
| native | 原生 Redis | 独立卷，只读，文件内容相同 | 显示 forkredis 引擎本身的成本 |

每个负载、每档缓存各跑三轮，交换执行顺序。同一个正常优化构建、相同缓存预算、相同数据和操作。导入、fork、挂载、预热均不计入性能结果。

## 输入和操作

| 负载 | 文件 | 循环数 | 每次读取 | 合计读取 |
|---|---|---:|---:|---:|
| tiny2 | 13 B、64 B 两个文件 | 512 | 整文件 | 39,424 B / 1,024 次 |
| small128 | 128 × 4 KiB | 16 | 4 KiB | 8 MiB / 2,048 次 |
| blocks8 | 8 × 1 MiB | 256 | 64 KiB，固定偏移序列 | 128 MiB / 2,048 次 |

每轮打开并枚举目录、查询一个文件属性，再逐文件打开、pread、关闭。每次读取检查实际内容。文件由固定种子生成，SHA 保存到 `run.json`。tiny2 仅用于定位，不代表真实 Agent 任务。

两档元数据缓存：`default` 的 attr/entry/dir-entry 缓存为 1 秒；`diagnostic` 为 0。其余参数一致：open-cache=0、noatime、数据缓存 128 MiB、buffer=64 MiB、预读/预取=0。所有读取使用 O_DIRECT，绕过内核数据页缓存，但保留 JuiceFS 数据缓存。因此 default 仅指默认元数据缓存时间，不是完整的默认挂载配置。

## 如何确认 lower 已热

每次重新挂载后，完整读取并校验本轮文件，再完整读一遍。第二遍必须同时满足：对象 GET=0、对象读取字节=0、FUSE 读取字节与输入相同，才能进入计时。计时完成后再次核对 GET=0、对象读取字节=0，且 FUSE 的读取次数和字节数等于负载。任一不符，该项失败并保留原始记录。

使用本机 `file://` 对象存储、本机独立 Redis，专门测已热数据下的客户端和元数据路径。不代表跨机 Redis、冷数据或带上层修改的结果。

## 执行

需要 Linux、Python 3.11+、FUSE、`redis-server`、`taskset`。二进制必须包含本分支的 forkredis。脚本自己启动一个只绑定 loopback 的 Redis，随机端口、256 MiB 上限，不清空其他 Redis。输出目录必须不存在。

```bash
python3 hack/lazy_clone_read_bench.py \
  --binary /path/to/juicefs \
  --source-commit BUILD_COMMIT \
  --out /srv/jfs-read-run-01 \
  --client-cpus 8-11 --redis-cpu 12 --driver-cpu 13 --pairs 3
python3 hack/lazy_clone_read_report.py /srv/jfs-read-run-01
```

CPU 编号需要按机器实际情况选择。客户端、Redis 和负载进程使用不同 CPU；亲和性不等于独占 CPU，`host.json` 保留宿主负载及压力信息。先用 `--quick --pairs 1` 运行 tiny2 预检，再使用新的输出目录执行正式测量。

新旧版本比较时，用 `--binary OLD --source-commit OLD_COMMIT --compare-binary NEW --compare-commit NEW_COMMIT`。每个对照内交替新旧二进制顺序，二者共享同一份初始数据，每次独立挂载和预热；默认共 108 项。`--profile default --fixture small128` 可只跑一档一负载。加 `--compare-cache-bytes 0` 关闭新版元数据缓存，测合批本身的效果。旧二进制的 URL 不增加新参数。原生 Redis 也使用两个二进制复测，用于检查环境波动。

## 输出

- `RESULTS.zh.md`、`summary.json`、`cell-summary.csv`：每轮总耗时、配对开销、各类操作的总耗时及 p50/p99、Redis 命令数和客户端/Redis CPU 时间。
- `cells/*/samples.ndjson.gz`：逐次目录、属性、open、pread、close 的耗时；计时结束后才写文件。
- `cells/*/before.prom`、`after.prom`：完整客户端指标，包括对象读、缓存命中和 FUSE 读取。
- `cells/*/redis-before.txt`、`redis-after.txt`：Redis commandstats 原文。包含 Lua 内部命令，不等于网络往返次数。
- `cells/*/warm.json`、`mount-config.json`、`process.json`、`host.json`：预热证明、实际配置和资源使用。
- `run.json`：二进制/脚本 SHA、源码提交、输入 SHA、所有命令及退出状态。

新版 `run.json` 中的 `binaries` 分别记录优化前后提交和二进制 SHA；每项结果带 `variant`，汇总不会混合两个版本。缓存命中、未命中、读请求批次数和实际缓存占用也保存到结果；原始 Prometheus 保留按记录/范围区分的指标。元数据缓存预算与数据缓存预算分别披露。

正常退出和异常退出都会卸载本脚本的挂载并停止自己启动的 Redis。保留目录和数据用于核查，不改其他挂载，不执行宿主 drop_caches。

客户端 `/proc` 采样使用挂载配置中的 worker PID，不使用前台监督进程 PID。汇总时从原始 Prometheus 恢复 worker CPU，因此旧版本采样记录也可正确汇总。
