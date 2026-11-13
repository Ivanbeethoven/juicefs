# forkredis 事务分段计时

| 版本 | 缓存档 | 负载 | 对照 | 总耗时 ms | 开始 ms | 查询 ms | 结束 ms | 提交 ms | 新校验 ms | Redis API 次数 |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|
| after | default | blocks8 | lower | 948.66 | 0.00 | 0.00 | 0.00 | 0.00 | 279.80 | 3073.00 |
| after | default | blocks8 | overlay | 949.40 | 0.00 | 0.00 | 0.00 | 0.00 | 276.60 | 3073.00 |
| after | default | small128 | lower | 590.65 | 0.00 | 0.00 | 0.00 | 0.00 | 186.46 | 2112.00 |
| after | default | small128 | overlay | 595.15 | 0.00 | 0.00 | 0.00 | 0.00 | 186.49 | 2112.00 |
| after | default | tiny2 | lower | 647.98 | 0.12 | 0.09 | 0.00 | 0.00 | 267.97 | 3075.00 |
| after | default | tiny2 | overlay | 671.89 | 0.58 | 0.16 | 0.00 | 0.00 | 288.46 | 3075.00 |
| before | default | blocks8 | lower | 984.06 | 0.00 | 0.00 | 0.00 | 0.00 | 298.16 | 3329.00 |
| before | default | blocks8 | overlay | 980.99 | 0.00 | 0.00 | 0.00 | 0.00 | 295.32 | 3329.00 |
| before | default | small128 | lower | 605.15 | 0.00 | 0.00 | 0.00 | 0.00 | 188.04 | 2128.00 |
| before | default | small128 | overlay | 611.75 | 0.00 | 0.00 | 0.00 | 0.00 | 189.89 | 2128.00 |
| before | default | tiny2 | lower | 697.69 | 0.12 | 0.10 | 0.00 | 0.00 | 310.21 | 3587.00 |
| before | default | tiny2 | overlay | 717.28 | 0.68 | 0.09 | 0.00 | 0.00 | 321.21 | 3587.00 |

每组为各次测量的中位数。分段为客户端累计耗时，包括连接等待、网络和 Redis 执行；并发操作不能直接相加解释墙钟耗时。body 包含查询，transaction 包含完整事务各段。

Redis API 次数为客户端发起的命令或 pipeline 次数，不包含重试产生的额外网络发送。Redis commandstats 包含 Lua 内部命令；其 calls 不能当成网络往返，Lua 与内部命令的 usec 不能重复相加。

原始逐次操作、Prometheus 文本和 Redis commandstats 留在 cells/。无计时指标的二进制标记 instrumented=false，不记作耗时为零。
