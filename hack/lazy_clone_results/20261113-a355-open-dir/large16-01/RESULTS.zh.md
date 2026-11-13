# JuiceFS Overlay 热读开销

单层对照与 Overlay 使用同一 forkredis 引擎、同一个已封存 lower 和同一二进制。单层只读；Overlay 保持可写、文件内容无修改。原生 Redis JuiceFS 单独列出，用于区分新引擎成本。

| 版本 | 元数据缓存 | 负载 | 单层 ms | Overlay ms | 配对开销中位数 | 各轮开销范围 | 原生 Redis ms |
|---|---|---|---:|---:|---:|---:|---:|
| after | default | many10000 | 8852.13 | 9775.19 | +10.4% | -9.0%–16.9% | 9937.66 |
| before | default | many10000 | 9066.76 | 10607.08 | +17.4% | 15.8%–28.8% | 9963.93 |

## 新旧二进制配对比较

| 缓存档 | 负载 | 对照 | 优化前 ms | 优化后 ms | 耗时减少（配对中位数） |
|---|---|---|---:|---:|---:|
| default | many10000 | lower | 9066.76 | 8852.13 | 2.4% |
| default | many10000 | native | 9963.93 | 9937.66 | -0.3% |
| default | many10000 | overlay | 10607.08 | 9775.19 | 16.3% |

表中耗时和配对开销均取中位数。每一对先算 Overlay/单层，再汇总；相对原生 Redis 的比值另存于 summary.json。负载固定，没有模型调用。

default：属性和目录项缓存 1 秒。diagnostic：这三项缓存为 0。两组均禁用 open-cache、预读和预取，数据缓存固定 128 MiB；O_DIRECT 绕过内核数据页缓存，仍使用 JuiceFS 数据缓存。因此这里不是默认完整挂载配置的吞吐测试。

全部计时区间对象 GET 与对象读取字节为 0，FUSE 读取次数和字节数与负载逐项相符。Redis 命令统计包含 Lua 内部命令，不能解释成网络往返次数。对象后端为本机 file://，Redis 也在本机；跨机 Redis、冷数据和带上层修改的性能不由本次结果覆盖。

客户端 CPU 从已保存的 before/after.prom 的 process_cpu_seconds_total 计算。早期采集脚本在 result.json/process.json 中记录的是前台监督进程 CPU，不能代表实际 FUSE worker；原始文件保留，汇总已改用 worker 的指标。

源码：`4f5a0ea25547e1becfbbb316dd9d6ca0537d6c00`；二进制 SHA-256：`28149916f1406395cff4a6b063def26936fea7d5f66720944627b17060a5aeb3`。

[逐次汇总](cell-summary.csv) · [分组及配对数据](summary.json) · [完整命令和环境](run.json)
