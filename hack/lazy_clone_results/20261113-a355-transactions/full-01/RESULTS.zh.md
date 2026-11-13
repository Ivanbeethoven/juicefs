# JuiceFS Overlay 热读开销

单层对照与 Overlay 使用同一 forkredis 引擎、同一个已封存 lower 和同一二进制。单层只读；Overlay 保持可写、文件内容无修改。原生 Redis JuiceFS 单独列出，用于区分新引擎成本。

| 版本 | 元数据缓存 | 负载 | 单层 ms | Overlay ms | 配对开销中位数 | 各轮开销范围 | 原生 Redis ms |
|---|---|---|---:|---:|---:|---:|---:|
| after | default | blocks8 | 982.63 | 1054.02 | +4.2% | 2.7%–7.3% | 926.12 |
| after | default | small128 | 605.72 | 613.20 | +1.2% | -0.1%–2.0% | 574.30 |
| after | default | tiny2 | 705.56 | 720.44 | +0.9% | -1.3%–2.1% | 665.73 |
| after | diagnostic | blocks8 | 3007.97 | 3046.04 | +0.8% | -0.4%–1.6% | 3093.13 |
| after | diagnostic | small128 | 2372.32 | 2423.47 | +1.4% | 1.3%–2.5% | 2457.37 |
| after | diagnostic | tiny2 | 2148.33 | 2175.74 | +1.3% | 0.5%–2.0% | 2200.74 |
| before | default | blocks8 | 1493.53 | 1510.87 | +1.2% | -1.6%–1.4% | 935.20 |
| before | default | small128 | 951.42 | 956.71 | +0.8% | 0.6%–1.1% | 568.47 |
| before | default | tiny2 | 1218.51 | 1221.06 | -0.4% | -1.2%–1.5% | 627.20 |
| before | diagnostic | blocks8 | 5571.15 | 5566.98 | +0.1% | -0.7%–3.6% | 3132.09 |
| before | diagnostic | small128 | 4445.24 | 4475.89 | +0.6% | 0.6%–1.1% | 2466.07 |
| before | diagnostic | tiny2 | 4090.85 | 4114.58 | +0.6% | 0.2%–1.2% | 2199.64 |

## 新旧二进制配对比较

| 缓存档 | 负载 | 对照 | 优化前 ms | 优化后 ms | 耗时减少（配对中位数） |
|---|---|---|---:|---:|---:|
| default | blocks8 | lower | 1493.53 | 982.63 | 34.2% |
| default | blocks8 | native | 935.20 | 926.12 | 1.0% |
| default | blocks8 | overlay | 1510.87 | 1054.02 | 30.2% |
| default | small128 | lower | 951.42 | 605.72 | 36.3% |
| default | small128 | native | 568.47 | 574.30 | -0.5% |
| default | small128 | overlay | 956.71 | 613.20 | 35.9% |
| default | tiny2 | lower | 1218.51 | 705.56 | 42.2% |
| default | tiny2 | native | 627.20 | 665.73 | -6.1% |
| default | tiny2 | overlay | 1221.06 | 720.44 | 41.0% |
| diagnostic | blocks8 | lower | 5571.15 | 3007.97 | 45.8% |
| diagnostic | blocks8 | native | 3132.09 | 3093.13 | 1.2% |
| diagnostic | blocks8 | overlay | 5566.98 | 3046.04 | 45.4% |
| diagnostic | small128 | lower | 4445.24 | 2372.32 | 46.4% |
| diagnostic | small128 | native | 2466.07 | 2457.37 | 0.7% |
| diagnostic | small128 | overlay | 4475.89 | 2423.47 | 46.1% |
| diagnostic | tiny2 | lower | 4090.85 | 2148.33 | 47.5% |
| diagnostic | tiny2 | native | 2199.64 | 2200.74 | -0.0% |
| diagnostic | tiny2 | overlay | 4114.58 | 2175.74 | 47.1% |

表中耗时和配对开销均取中位数。每一对先算 Overlay/单层，再汇总；相对原生 Redis 的比值另存于 summary.json。负载固定，没有模型调用。

default：属性和目录项缓存 1 秒。diagnostic：这三项缓存为 0。两组均禁用 open-cache、预读和预取，数据缓存固定 128 MiB；O_DIRECT 绕过内核数据页缓存，仍使用 JuiceFS 数据缓存。因此这里不是默认完整挂载配置的吞吐测试。

全部计时区间对象 GET 与对象读取字节为 0，FUSE 读取次数和字节数与负载逐项相符。Redis 命令统计包含 Lua 内部命令，不能解释成网络往返次数。对象后端为本机 file://，Redis 也在本机；跨机 Redis、冷数据和带上层修改的性能不由本次结果覆盖。

客户端 CPU 从已保存的 before/after.prom 的 process_cpu_seconds_total 计算。早期采集脚本在 result.json/process.json 中记录的是前台监督进程 CPU，不能代表实际 FUSE worker；原始文件保留，汇总已改用 worker 的指标。

源码：`943e569cfb838f93ae64cc2df4b4d86bd78400a7`；二进制 SHA-256：`ba6ec8a5c48693144971b8d2e3a7593c2c54a8bd4a8f4725c511a0295ba0f151`。

[逐次汇总](cell-summary.csv) · [分组及配对数据](summary.json) · [完整命令和环境](run.json)
