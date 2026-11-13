# forkredis 事务分段计时

| 版本 | 缓存档 | 负载 | 对照 | 总耗时 ms | 开始 ms | 查询 ms | 结束 ms | 提交 ms | 新校验 ms | Redis API 次数 |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|
| after | default | many10000 | lower | 8948.02 | 0.00 | 0.00 | 0.00 | 0.00 | 3652.36 | 39986.00 |
| after | default | many10000 | overlay | 10157.67 | 435.70 | 607.32 | 346.27 | 0.19 | 3347.30 | 50672.00 |
| before | default | many10000 | lower | 9078.21 | 0.00 | 0.00 | 0.00 | 0.00 | 3652.88 | 39992.00 |
| before | default | many10000 | overlay | 10993.93 | 613.63 | 840.74 | 485.28 | 0.18 | 3233.51 | 54501.00 |

每组为各次测量的中位数。分段为客户端累计耗时，包括连接等待、网络和 Redis 执行；并发操作不能直接相加解释墙钟耗时。body 包含查询，transaction 包含完整事务各段。

Redis API 次数为客户端发起的命令或 pipeline 次数，不包含重试产生的额外网络发送。Redis commandstats 包含 Lua 内部命令；其 calls 不能当成网络往返，Lua 与内部命令的 usec 不能重复相加。

原始逐次操作、Prometheus 文本和 Redis commandstats 留在 cells/。无计时指标的二进制标记 instrumented=false，不记作耗时为零。
