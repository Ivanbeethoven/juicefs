# forkredis 事务分段计时

| 版本 | 缓存档 | 负载 | 对照 | 总耗时 ms | 开始 ms | 查询 ms | 结束 ms | 提交 ms | 新校验 ms | Redis API 次数 |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|
| after | default | blocks8 | lower | 982.63 | 0.00 | 0.00 | 0.00 | 0.00 | 301.58 | 3329.00 |
| after | default | blocks8 | overlay | 1054.02 | 0.00 | 0.00 | 0.00 | 0.00 | 319.23 | 3329.00 |
| after | default | small128 | lower | 605.72 | 0.00 | 0.00 | 0.00 | 0.00 | 190.74 | 2128.00 |
| after | default | small128 | overlay | 613.20 | 0.00 | 0.00 | 0.00 | 0.00 | 191.70 | 2128.00 |
| after | default | tiny2 | lower | 705.56 | 0.14 | 0.07 | 0.00 | 0.00 | 313.59 | 3587.00 |
| after | default | tiny2 | overlay | 720.44 | 0.15 | 0.16 | 0.00 | 0.00 | 325.87 | 3587.00 |
| after | diagnostic | blocks8 | lower | 3007.97 | 0.00 | 0.00 | 0.00 | 0.00 | 1526.49 | 17665.00 |
| after | diagnostic | blocks8 | overlay | 3046.04 | 0.00 | 0.00 | 0.00 | 0.00 | 1535.26 | 17665.00 |
| after | diagnostic | small128 | lower | 2372.32 | 0.00 | 0.00 | 0.00 | 0.00 | 1252.30 | 14544.00 |
| after | diagnostic | small128 | overlay | 2423.47 | 0.00 | 0.00 | 0.00 | 0.00 | 1265.08 | 14544.00 |
| after | diagnostic | tiny2 | lower | 2148.33 | 0.13 | 0.18 | 0.00 | 0.00 | 1194.26 | 13832.00 |
| after | diagnostic | tiny2 | overlay | 2175.74 | 0.16 | 0.14 | 0.00 | 0.00 | 1192.12 | 13832.00 |
| before | default | blocks8 | lower | 1493.53 | 418.70 | 0.00 | 351.23 | 0.00 | 0.00 | 6658.00 |
| before | default | blocks8 | overlay | 1510.87 | 418.39 | 0.00 | 351.99 | 0.00 | 0.00 | 6658.00 |
| before | default | small128 | lower | 951.42 | 274.65 | 0.00 | 229.75 | 0.00 | 0.00 | 4468.00 |
| before | default | small128 | overlay | 956.71 | 273.16 | 0.00 | 228.98 | 0.00 | 0.00 | 4468.00 |
| before | default | tiny2 | lower | 1218.51 | 443.19 | 0.09 | 358.80 | 0.00 | 0.00 | 7173.00 |
| before | default | tiny2 | overlay | 1221.06 | 431.84 | 0.09 | 368.95 | 0.00 | 0.00 | 7173.00 |
| before | diagnostic | blocks8 | lower | 5571.15 | 2166.11 | 0.00 | 1791.54 | 0.00 | 0.00 | 35332.00 |
| before | diagnostic | blocks8 | overlay | 5566.98 | 2141.35 | 0.00 | 1779.84 | 0.00 | 0.00 | 35332.00 |
| before | diagnostic | small128 | lower | 4445.24 | 1753.47 | 0.00 | 1454.29 | 0.00 | 0.00 | 29090.00 |
| before | diagnostic | small128 | overlay | 4475.89 | 1753.18 | 0.00 | 1456.06 | 0.00 | 0.00 | 29090.00 |
| before | diagnostic | tiny2 | lower | 4090.85 | 1660.05 | 0.10 | 1377.81 | 0.00 | 0.00 | 27661.00 |
| before | diagnostic | tiny2 | overlay | 4114.58 | 1660.12 | 0.16 | 1376.79 | 0.00 | 0.00 | 27661.00 |

每组为各次测量的中位数。分段为客户端累计耗时，包括连接等待、网络和 Redis 执行；并发操作不能直接相加解释墙钟耗时。body 包含查询，transaction 包含完整事务各段。

Redis API 次数为客户端发起的命令或 pipeline 次数，不包含重试产生的额外网络发送。Redis commandstats 包含 Lua 内部命令；其 calls 不能当成网络往返，Lua 与内部命令的 usec 不能重复相加。

原始逐次操作、Prometheus 文本和 Redis commandstats 留在 cells/。无计时指标的二进制标记 instrumented=false，不记作耗时为零。
