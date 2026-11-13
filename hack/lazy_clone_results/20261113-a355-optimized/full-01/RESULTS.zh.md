# JuiceFS Overlay 热读开销

单层对照与 Overlay 使用同一 forkredis 引擎、同一个已封存 lower 和同一二进制。单层只读；Overlay 保持可写、文件内容无修改。原生 Redis JuiceFS 单独列出，用于区分新引擎成本。

| 版本 | 元数据缓存 | 负载 | 单层 ms | Overlay ms | 配对开销中位数 | 各轮开销范围 | 原生 Redis ms |
|---|---|---|---:|---:|---:|---:|---:|
| after | default | blocks8 | 1528.11 | 1495.68 | -2.2% | -2.4%–0.8% | 934.16 |
| after | default | small128 | 944.15 | 954.04 | +1.1% | 1.0%–1.3% | 573.27 |
| after | default | tiny2 | 1211.49 | 1218.41 | +0.1% | 0.0%–1.8% | 644.32 |
| after | diagnostic | blocks8 | 5517.51 | 5553.47 | +0.8% | -0.0%–1.2% | 3108.64 |
| after | diagnostic | small128 | 4421.28 | 4472.89 | +1.0% | 0.8%–1.3% | 2486.62 |
| after | diagnostic | tiny2 | 4061.21 | 4086.97 | +0.7% | -0.1%–1.2% | 2215.59 |
| before | default | blocks8 | 2062.23 | 2609.69 | +26.8% | 26.1%–26.8% | 932.13 |
| before | default | small128 | 1438.95 | 1991.41 | +38.4% | 37.8%–39.2% | 572.76 |
| before | default | tiny2 | 1604.97 | 1997.90 | +24.5% | 22.9%–25.8% | 670.90 |
| before | diagnostic | blocks8 | 7626.55 | 9667.65 | +26.8% | 26.5%–27.1% | 3136.00 |
| before | diagnostic | small128 | 6220.82 | 7947.77 | +27.8% | 27.7%–27.9% | 2483.73 |
| before | diagnostic | tiny2 | 5554.56 | 6982.93 | +26.4% | 25.5%–26.5% | 2202.05 |

## 新旧二进制配对比较

| 缓存档 | 负载 | 对照 | 优化前 ms | 优化后 ms | 耗时减少（配对中位数） |
|---|---|---|---:|---:|---:|
| default | blocks8 | lower | 2062.23 | 1528.11 | 26.0% |
| default | blocks8 | native | 932.13 | 934.16 | 0.1% |
| default | blocks8 | overlay | 2609.69 | 1495.68 | 42.7% |
| default | small128 | lower | 1438.95 | 944.15 | 34.4% |
| default | small128 | native | 572.76 | 573.27 | 0.1% |
| default | small128 | overlay | 1991.41 | 954.04 | 52.1% |
| default | tiny2 | lower | 1604.97 | 1211.49 | 24.5% |
| default | tiny2 | native | 670.90 | 644.32 | 1.7% |
| default | tiny2 | overlay | 1997.90 | 1218.41 | 39.3% |
| diagnostic | blocks8 | lower | 7626.55 | 5517.51 | 27.7% |
| diagnostic | blocks8 | native | 3136.00 | 3108.64 | 0.6% |
| diagnostic | blocks8 | overlay | 9667.65 | 5553.47 | 42.4% |
| diagnostic | small128 | lower | 6220.82 | 4421.28 | 28.7% |
| diagnostic | small128 | native | 2483.73 | 2486.62 | -0.1% |
| diagnostic | small128 | overlay | 7947.77 | 4472.89 | 43.7% |
| diagnostic | tiny2 | lower | 5554.56 | 4061.21 | 26.8% |
| diagnostic | tiny2 | native | 2202.05 | 2215.59 | -0.6% |
| diagnostic | tiny2 | overlay | 6982.93 | 4086.97 | 41.6% |

表中耗时和配对开销均取中位数。每一对先算 Overlay/单层，再汇总；相对原生 Redis 的比值另存于 summary.json。负载固定，没有模型调用。

default：属性和目录项缓存 1 秒。diagnostic：这三项缓存为 0。两组均禁用 open-cache、预读和预取，数据缓存固定 128 MiB；O_DIRECT 绕过内核数据页缓存，仍使用 JuiceFS 数据缓存。因此这里不是默认完整挂载配置的吞吐测试。

全部计时区间对象 GET 与对象读取字节为 0，FUSE 读取次数和字节数与负载逐项相符。Redis 命令统计包含 Lua 内部命令，不能解释成网络往返次数。对象后端为本机 file://，Redis 也在本机；跨机 Redis、冷数据和带上层修改的性能不由本次结果覆盖。

客户端 CPU 从已保存的 before/after.prom 的 process_cpu_seconds_total 计算。早期采集脚本在 result.json/process.json 中记录的是前台监督进程 CPU，不能代表实际 FUSE worker；原始文件保留，汇总已改用 worker 的指标。

源码：`7aaafbe3c40b1b377accffd00755a7259f4f44e9`；二进制 SHA-256：`644c5a86ba1e7eff9efced22fa65138b881fdbe7eb9fd2e1e77d46462d01f9a7`。

[逐次汇总](cell-summary.csv) · [分组及配对数据](summary.json) · [完整命令和环境](run.json)
