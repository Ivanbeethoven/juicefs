# JuiceFS Overlay 热读开销

单层对照与 Overlay 使用同一 forkredis 引擎、同一个已封存 lower 和同一二进制。单层只读；Overlay 保持可写、文件内容无修改。原生 Redis JuiceFS 单独列出，用于区分新引擎成本。

| 版本 | 元数据缓存 | 负载 | 单层 ms | Overlay ms | 配对开销中位数 | 各轮开销范围 | 原生 Redis ms |
|---|---|---|---:|---:|---:|---:|---:|
| after | default | small128 | 956.95 | 968.32 | +1.1% | 0.3%–1.8% | 576.64 |
| before | default | small128 | 955.69 | 968.62 | +1.5% | 1.3%–2.3% | 574.14 |

## 新旧二进制配对比较

| 缓存档 | 负载 | 对照 | 优化前 ms | 优化后 ms | 耗时减少（配对中位数） |
|---|---|---|---:|---:|---:|
| default | small128 | lower | 955.69 | 956.95 | -0.2% |
| default | small128 | native | 574.14 | 576.64 | 0.4% |
| default | small128 | overlay | 968.62 | 968.32 | 0.0% |

表中耗时和配对开销均取中位数。每一对先算 Overlay/单层，再汇总；相对原生 Redis 的比值另存于 summary.json。负载固定，没有模型调用。

default：属性和目录项缓存 1 秒。diagnostic：这三项缓存为 0。两组均禁用 open-cache、预读和预取，数据缓存固定 128 MiB；O_DIRECT 绕过内核数据页缓存，仍使用 JuiceFS 数据缓存。因此这里不是默认完整挂载配置的吞吐测试。

全部计时区间对象 GET 与对象读取字节为 0，FUSE 读取次数和字节数与负载逐项相符。Redis 命令统计包含 Lua 内部命令，不能解释成网络往返次数。对象后端为本机 file://，Redis 也在本机；跨机 Redis、冷数据和带上层修改的性能不由本次结果覆盖。

客户端 CPU 从已保存的 before/after.prom 的 process_cpu_seconds_total 计算。早期采集脚本在 result.json/process.json 中记录的是前台监督进程 CPU，不能代表实际 FUSE worker；原始文件保留，汇总已改用 worker 的指标。

源码：`c8a4035a17d79ebdffe2876f85b1ce66c6ee5430`；二进制 SHA-256：`f732f71bdf9b8032a9f9fc1266ec3d928d40bdac809386b2893cb4b827d546ce`。

[逐次汇总](cell-summary.csv) · [分组及配对数据](summary.json) · [完整命令和环境](run.json)
