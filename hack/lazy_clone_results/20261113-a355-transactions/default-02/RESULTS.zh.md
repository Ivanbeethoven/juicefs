# JuiceFS Overlay 热读开销

单层对照与 Overlay 使用同一 forkredis 引擎、同一个已封存 lower 和同一二进制。单层只读；Overlay 保持可写、文件内容无修改。原生 Redis JuiceFS 单独列出，用于区分新引擎成本。

| 版本 | 元数据缓存 | 负载 | 单层 ms | Overlay ms | 配对开销中位数 | 各轮开销范围 | 原生 Redis ms |
|---|---|---|---:|---:|---:|---:|---:|
| after | default | blocks8 | 994.55 | 993.89 | -0.1% | -0.9%–1.3% | 925.27 |
| after | default | small128 | 612.57 | 618.59 | +0.9% | 0.4%–2.5% | 574.07 |
| after | default | tiny2 | 707.40 | 736.00 | +4.0% | -3.3%–4.4% | 654.24 |
| before | default | blocks8 | 1494.27 | 1498.77 | +0.3% | -0.3%–0.9% | 935.46 |
| before | default | small128 | 952.38 | 962.91 | +0.6% | 0.2%–1.3% | 572.29 |
| before | default | tiny2 | 1203.49 | 1206.20 | +0.8% | 0.0%–2.5% | 650.82 |

## 新旧二进制配对比较

| 缓存档 | 负载 | 对照 | 优化前 ms | 优化后 ms | 耗时减少（配对中位数） |
|---|---|---|---:|---:|---:|
| default | blocks8 | lower | 1494.27 | 994.55 | 33.6% |
| default | blocks8 | native | 935.46 | 925.27 | -0.7% |
| default | blocks8 | overlay | 1498.77 | 993.89 | 33.7% |
| default | small128 | lower | 952.38 | 612.57 | 36.0% |
| default | small128 | native | 572.29 | 574.07 | -0.0% |
| default | small128 | overlay | 962.91 | 618.59 | 35.8% |
| default | tiny2 | lower | 1203.49 | 707.40 | 41.0% |
| default | tiny2 | native | 650.82 | 654.24 | -0.6% |
| default | tiny2 | overlay | 1206.20 | 736.00 | 38.9% |

表中耗时和配对开销均取中位数。每一对先算 Overlay/单层，再汇总；相对原生 Redis 的比值另存于 summary.json。负载固定，没有模型调用。

default：属性和目录项缓存 1 秒。diagnostic：这三项缓存为 0。两组均禁用 open-cache、预读和预取，数据缓存固定 128 MiB；O_DIRECT 绕过内核数据页缓存，仍使用 JuiceFS 数据缓存。因此这里不是默认完整挂载配置的吞吐测试。

全部计时区间对象 GET 与对象读取字节为 0，FUSE 读取次数和字节数与负载逐项相符。Redis 命令统计包含 Lua 内部命令，不能解释成网络往返次数。对象后端为本机 file://，Redis 也在本机；跨机 Redis、冷数据和带上层修改的性能不由本次结果覆盖。

客户端 CPU 从已保存的 before/after.prom 的 process_cpu_seconds_total 计算。早期采集脚本在 result.json/process.json 中记录的是前台监督进程 CPU，不能代表实际 FUSE worker；原始文件保留，汇总已改用 worker 的指标。

源码：`943e569cfb838f93ae64cc2df4b4d86bd78400a7`；二进制 SHA-256：`ba6ec8a5c48693144971b8d2e3a7593c2c54a8bd4a8f4725c511a0295ba0f151`。

[逐次汇总](cell-summary.csv) · [分组及配对数据](summary.json) · [完整命令和环境](run.json)
