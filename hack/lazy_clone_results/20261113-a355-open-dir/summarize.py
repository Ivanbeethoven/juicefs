#!/usr/bin/env python3
"""Regenerate the cross-run tables from the checked-in per-run summaries."""
import json
from pathlib import Path


def main():
    root = Path(__file__).resolve().parent
    rows = []
    phases = []
    operations = []
    for name, capacity in [('small-01', 16), ('large16-01', 16), ('large64-01', 64)]:
        summary = json.loads((root/name/'summary.json').read_text())
        txn = json.loads((root/name/'txn-summary.json').read_text())
        for after in summary['groups']:
            if after['variant'] != 'after':
                continue
            before = next(g for g in summary['groups'] if g['variant'] == 'before'
                          and g['fixture'] == after['fixture'])
            comparison = next(c for c in summary['version_comparisons']
                              if c['fixture'] == after['fixture'] and c['arm'] == 'overlay')
            rows.append(dict(run=name, fixture=after['fixture'], cache_mib=capacity,
                             before_ms=before['arms']['overlay']['wall_ms'],
                             after_ms=after['arms']['overlay']['wall_ms'],
                             native_ms=after['arms']['native']['wall_ms'],
                             paired_reduction_percent=comparison['paired_time_reduction_percent'],
                             reduction_percent=comparison['median_time_reduction_percent'],
                             before_native_ratio=before['median_overlay_vs_native_ratio'],
                             after_native_ratio=after['median_overlay_vs_native_ratio'],
                             after_native_ratios=after['overlay_vs_native_ratios']))
            for group in (before, after):
                arm = group['arms']['overlay']
                t = next(g for g in txn['groups'] if g['variant'] == group['variant']
                         and g['fixture'] == group['fixture'] and g['arm'] == 'overlay')['median']
                identity = dict(run=name, fixture=after['fixture'], variant=group['variant'])
                phases.append(identity | {k: t[k] for k in (
                    'cached_prepare_ms', 'cached_validate_ms', 'cached_validate_calls',
                    'begin_ms', 'data_io_ms', 'end_ms', 'commit_ms', 'commit_calls',
                    'publish_ms', 'redis_api_calls', 'cache_misses', 'cached_read_fallbacks',
                    'redis_zrevrangebyscore_calls', 'redis_zrangebylex_calls')}
                    | {'cache_mib': arm['forkredis_cache_bytes']/2**20})
                operations.append(identity | {k: arm[k] for k in (
                    'open_list_total_ms', 'open_total_ms', 'pread_total_ms',
                    'stat_total_ms', 'close_total_ms', 'open_p50_us', 'open_p99_us',
                    'open_list_p50_us', 'open_list_p99_us')})
    (root/'comparison.json').write_text(json.dumps(dict(totals=rows, phases=phases,
        operations=operations), indent=2)+'\n')
    lines = ['# 新旧版本与原生对照', '',
             '耗时为三次中位数，降幅和倍数先逐对计算再取中位数。单位 ms。', '',
             '| 负载 | 缓存 MiB | 优化前 | 优化后 | 原生 | 耗时下降 | 优化前/原生 | 优化后/原生 | 新版/原生范围 |',
             '|---|---:|---:|---:|---:|---:|---:|---:|---:|']
    for r in rows:
        ratios = r['after_native_ratios']
        lines.append(f"| {r['fixture']} | {r['cache_mib']} | {r['before_ms']:.2f} | {r['after_ms']:.2f} | {r['native_ms']:.2f} | {r['reduction_percent']:.2f}% | {r['before_native_ratio']:.3f} | {r['after_native_ratio']:.3f} | {min(ratios):.3f}–{max(ratios):.3f} |")
    lines += ['', '## 调用耗时', '', '每项为一轮负载中该类操作的累计耗时，再对三次取中位数。', '',
              '| 组 | 负载 | 版本 | 打开文件 ms | 打开并列目录 ms | pread ms |',
              '|---|---|---|---:|---:|---:|']
    for r in operations:
        lines.append(f"| {r['run']} | {r['fixture']} | {r['variant']} | {r['open_total_ms']:.2f} | {r['open_list_total_ms']:.2f} | {r['pread_total_ms']:.2f} |")
    lines += ['', '## 元数据路径耗时', '',
              '单位 ms；客户端累计时间包含等待。内部阶段有嵌套，不与墙钟时间直接相加。', '',
              '| 组/负载 | 版本 | 本地缓存收集 | 版本确认 | 开始事务 | 查询 | 结束事务 | 写提交 | 缓存发布 | Redis API 次数 |',
              '|---|---|---:|---:|---:|---:|---:|---:|---:|---:|']
    for r in phases:
        values = ' | '.join(f'{r[k]:.2f}' for k in ('cached_prepare_ms', 'cached_validate_ms',
            'begin_ms', 'data_io_ms', 'end_ms', 'commit_ms', 'publish_ms', 'redis_api_calls'))
        lines.append(f"| {r['run']}/{r['fixture']} | {r['variant']} | {values} |")
    lines += ['', 'Redis API 次数为客户端命令或 pipeline 的执行次数。Redis commandstats 包含 Lua 内部命令，不能直接当网络往返；原生引擎没有这些阶段指标，详见各组 txn-cells.csv。', '']
    (root/'COMPARISON.zh.md').write_text('\n'.join(lines))
    print(json.dumps(rows, indent=2))


if __name__ == '__main__':
    main()
