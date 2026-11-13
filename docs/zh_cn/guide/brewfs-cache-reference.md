# 给 BrewFS 的缓存实现参考

仓库：[Ivanbeethoven/juicefs](https://github.com/Ivanbeethoven/juicefs)，分支 `codex/lazy-workspace-fork-20261113`。这里实现的是独立子工作区挂载：父层封存只读，子层可写，fork 不复制整棵元数据树。

## 直接看这些位置

| 文件 / 函数 | 解决什么问题 |
|---|---|
| `pkg/meta/tkv_forkredis_cache.go` | 按层、版本、键或精确范围缓存；记录缺失、空范围、墓碑；按字节限制 LRU 容量 |
| `pkg/meta/tkv_forkredis_read.go: cachedRead` | 在临时变量中收集完整缓存结果，再一次确认当前版本；缺项或版本变化回退 |
| `pkg/meta/tkv_forkredis.go: readBatch` | 缓存读取不创建 Redis pipeline 和中间 map；保留墓碑优先及返回值副本 |
| `pkg/meta/tkv.go: getDirFetcher` | 一页目录项和对应属性放在同一个缓存读取中确认；成功后才更新 open-file/chunk 缓存 |
| `pkg/meta/tkv_forkredis.go: transaction` | 缓存未命中的读取仍登记快照；写入仍校验版本、超时和封存状态，原子提交 |
| `pkg/meta/tkv_forkredis_test.go` | 并发修改、墓碑、过期快照、写者拒绝、目录分页及属性缓存发布的回归测试 |

## 可以借鉴的做法

1. 缓存“上层没有这条记录”，但必须绑定上层版本。不能只按 inode 或路径缓存不存在。
2. 完整结果先收集到本地临时变量；最后一次远端版本检查通过前，不向调用者返回目录项，不更新属性缓存，不触发基于这些属性的 chunk 缓存变更。
3. 同一页的目录项和属性合并确认。缓存不全时丢弃临时结果，走原来的事务，不重复追加目录项。
4. 热缓存路径直接解码缓存记录，不构造不会发送的网络请求对象和临时查询 map。
5. 写操作保留原来的原子提交与旧写者拒绝；这里没有异步元数据写回或放宽读一致性。

这些做法依赖本实现的两个条件：旧版本记录不可变；父层已封存，只有当前分支头会前进。因此缓存里完整的同版本结果可以在一次分支头检查处确定。移植到 BrewFS 时，应对应其 workspace、层版本和 fencing 规则；不能把一次检查变成任意时长的跨操作有效期。

## 验证与复测

上轮 [A355 事务优化结果](../../../hack/lazy_clone_results/20261113-a355-transactions/README.zh.md)包含源码提交、二进制 SHA、逐次耗时、Redis commandstats 和全部运行命令。

本轮继续合并目录读取，并增加真实的 10,000 文件输入：

```bash
# 专用 Redis，正确性检查及 FUSE 挂载验证
JFS_FORK_REDIS_TEST_ADDR=127.0.0.1:6379 bash hack/lazy_clone_check.sh /tmp/new-check

# 10,000 × 4 KiB，单目录跨越 4096 项分页，2 遍共 20,000 次读取
python3 hack/lazy_clone_read_bench.py \
  --binary /path/to/before --source-commit BEFORE_COMMIT \
  --compare-binary /path/to/after --compare-commit AFTER_COMMIT \
  --fixture many10000 --profile default --pairs 3 \
  --metadata-cache-bytes 16777216 --out /srv/new-run
python3 hack/lazy_clone_txn_report.py /srv/new-run
```

再以 `--metadata-cache-bytes 67108864` 测相同输入，区分缓存工作集不足与每次操作本身的开销。该参数同时作用于优化前后，原生 Redis 的其他挂载参数保持一致。结果需同时记录缓存缺项、Redis 请求次数和 lower 数据已热的证明。

缓存仍默认 16 MiB；1 万文件测试不会只把操作数增加到 1 万，也不会只扩大新版缓存来取得优势。最终实测结果写入 `hack/lazy_clone_results/`，不覆盖历史结果。
