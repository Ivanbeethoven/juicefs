# forkredis 事务分段计时

| 版本 | 缓存档 | 负载 | 对照 | 总耗时 ms | 开始 ms | 查询 ms | 结束 ms | 提交 ms | 新校验 ms | Redis API 次数 |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|
| after | default | blocks8 | lower | 994.55 | 0.00 | 0.00 | 0.00 | 0.00 | 304.24 | 3329.00 |
| after | default | blocks8 | overlay | 993.89 | 0.00 | 0.00 | 0.00 | 0.00 | 301.75 | 3329.00 |
| after | default | small128 | lower | 612.57 | 0.00 | 0.00 | 0.00 | 0.00 | 191.72 | 2128.00 |
| after | default | small128 | overlay | 618.59 | 0.00 | 0.00 | 0.00 | 0.00 | 191.04 | 2128.00 |
| after | default | tiny2 | lower | 707.40 | 0.11 | 0.08 | 0.00 | 0.00 | 315.54 | 3587.00 |
| after | default | tiny2 | overlay | 736.00 | 0.56 | 0.07 | 0.00 | 0.00 | 330.67 | 3587.00 |
| before | default | blocks8 | lower | 1494.27 | 418.89 | 0.00 | 346.78 | 0.00 | 0.00 | 6658.00 |
| before | default | blocks8 | overlay | 1498.77 | 415.78 | 0.00 | 348.01 | 0.00 | 0.00 | 6658.00 |
| before | default | small128 | lower | 952.38 | 273.11 | 0.00 | 228.31 | 0.00 | 0.00 | 4468.00 |
| before | default | small128 | overlay | 962.91 | 272.76 | 0.00 | 228.81 | 0.00 | 0.00 | 4468.00 |
| before | default | tiny2 | lower | 1203.49 | 428.92 | 0.07 | 355.40 | 0.00 | 0.00 | 7173.00 |
| before | default | tiny2 | overlay | 1206.20 | 428.50 | 0.10 | 353.97 | 0.00 | 0.00 | 7173.00 |

每组为各次测量的中位数。分段为客户端累计耗时，包括连接等待、网络和 Redis 执行；并发操作不能直接相加解释墙钟耗时。body 包含查询，transaction 包含完整事务各段。

Redis API 次数为客户端发起的命令或 pipeline 次数，不包含重试产生的额外网络发送。Redis commandstats 包含 Lua 内部命令；其 calls 不能当成网络往返，Lua 与内部命令的 usec 不能重复相加。

原始逐次操作、Prometheus 文本和 Redis commandstats 留在 cells/。无计时指标的二进制标记 instrumented=false，不记作耗时为零。
