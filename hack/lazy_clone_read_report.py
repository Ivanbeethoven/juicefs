#!/usr/bin/env python3
"""Summarize a completed lazy_clone_read_bench.py run without changing raw data."""
import argparse
import csv
import gzip
import json
import math
from pathlib import Path
import re
import statistics


def percentile(values, quantile):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered)*quantile)-1)]


def client_cpu(path):
    values = re.findall(r'^juicefs_process_cpu_seconds_total(?:\{[^}]*\})? ([0-9.eE+-]+)$',
                        path.read_text(), re.MULTILINE)
    assert len(values) == 1, 'missing or ambiguous client CPU metric'
    return float(values[0])


def summarize(root):
    run = json.loads((root/'run.json').read_text())
    assert run['passed'] and run['mounts_closed'] and run['redis_stopped']
    cells = run['cells']
    output = []
    for cell in cells:
        assert cell['valid']
        assert cell['observed']['object_gets'] == cell['observed']['object_get_bytes'] == 0
        rows = []
        with gzip.open(root/'cells'/cell['label']/'samples.ndjson.gz', 'rt') as stream:
            rows = [json.loads(line) for line in stream]
        assert sum(row['bytes'] for row in rows) == cell['read_bytes']
        assert sum(len(row['pread_ns']) for row in rows) == cell['read_ops']
        assert cell['observed']['fuse_read_ops'] == cell['read_ops']
        assert cell['observed']['fuse_read_bytes'] == cell['read_bytes']
        flat = {key: cell[key] for key in ['label', 'profile', 'fixture', 'arm', 'pair', 'read_ops', 'read_bytes']}
        flat['variant'] = cell.get('variant', 'before')
        flat['wall_ms'] = cell['wall_ns']/1e6
        # Older runs sampled the foreground supervisor PID. The saved metrics
        # belong to the actual FUSE worker, so derive its CPU without rewriting raw data.
        cell_dir = root/'cells'/cell['label']
        flat['fuse_cpu_seconds'] = client_cpu(cell_dir/'after.prom') - client_cpu(cell_dir/'before.prom')
        assert flat['fuse_cpu_seconds'] >= 0
        flat['redis_cpu_seconds'] = cell.get('redis_cpu_seconds')
        flat['redis_commands_including_lua'] = sum(value for key, value in cell['redis_commands'].items() if key != 'info')
        flat['redis_zrangebylex'] = cell['redis_commands'].get('zrangebylex', 0)
        flat['redis_zrevrangebyscore'] = cell['redis_commands'].get('zrevrangebyscore', 0)
        for name in ['forkredis_cache_hits_total', 'forkredis_cache_misses_total', 'forkredis_read_batches_total', 'forkredis_cache_bytes']:
            flat[name] = cell['observed'].get(name, 0)
        for key in ['open_list_ns', 'stat_ns', 'open_ns', 'pread_ns', 'close_ns']:
            values = []
            for row in rows:
                values.extend(row[key] if isinstance(row[key], list) else [row[key]])
            assert sum(values) == cell['timing_ns'][key]
            name = key.removesuffix('_ns')
            flat[name+'_total_ms'] = sum(values)/1e6
            flat[name+'_p50_us'] = statistics.median(values)/1e3
            flat[name+'_p99_us'] = percentile(values, .99)/1e3
        output.append(flat)
    with (root/'cell-summary.csv').open('w', newline='') as stream:
        writer = csv.DictWriter(stream, fieldnames=list(output[0]))
        writer.writeheader()
        writer.writerows(output)
    groups = []
    for variant, profile, fixture in sorted({(row['variant'], row['profile'], row['fixture']) for row in output}):
        selected = [row for row in output if row['variant'] == variant and row['profile'] == profile and row['fixture'] == fixture]
        by_pair = {}
        for row in selected:
            arms = by_pair.setdefault(row['pair'], {})
            assert row['arm'] not in arms, 'duplicate arm/pair'
            arms[row['arm']] = row
        assert all(set(arms) == {'lower', 'overlay', 'native'} for arms in by_pair.values())
        ratios = [(arms['overlay']['wall_ms']/arms['lower']['wall_ms']-1)*100 for arms in by_pair.values()]
        native_ratios = [arms['overlay']['wall_ms']/arms['native']['wall_ms'] for arms in by_pair.values()]
        arm_summary = {}
        for arm in ['lower', 'overlay', 'native']:
            rows = [row for row in selected if row['arm'] == arm]
            arm_summary[arm] = {key: statistics.median([row[key] for row in rows])
                                for key in rows[0] if isinstance(rows[0][key], (float, int)) and key != 'pair'}
        groups.append({'variant': variant, 'profile': profile, 'fixture': fixture, 'pairs': len(by_pair),
                       'paired_overhead_percent': ratios, 'median_overhead_percent': statistics.median(ratios),
                       'overlay_vs_native_ratios': native_ratios,
                       'median_overlay_vs_native_ratio': statistics.median(native_ratios),
                       'arms': arm_summary})
    comparisons = []
    if len(run.get('binaries', {})) == 2:
        for profile, fixture, arm in sorted({(row['profile'], row['fixture'], row['arm']) for row in output}):
            pairs = {}
            for row in output:
                if (row['profile'], row['fixture'], row['arm']) == (profile, fixture, arm):
                    pairs.setdefault(row['pair'], {})[row['variant']] = row
            assert all(set(versions) == {'before', 'after'} for versions in pairs.values())
            reduction = [(1-v['after']['wall_ms']/v['before']['wall_ms'])*100 for v in pairs.values()]
            comparisons.append({'profile': profile, 'fixture': fixture, 'arm': arm,
                                'before_ms': statistics.median(v['before']['wall_ms'] for v in pairs.values()),
                                'after_ms': statistics.median(v['after']['wall_ms'] for v in pairs.values()),
                                'paired_time_reduction_percent': reduction,
                                'median_time_reduction_percent': statistics.median(reduction)})
    summary = {'source_commit': run['source_commit'], 'binary_sha256': run['binary_sha256'],
               'script_sha256': run['script_sha256'], 'host': run['uname'][1], 'cells': len(cells),
               'all_cells_hot': True, 'all_reads_reached_fuse': True,
               'groups': groups, 'version_comparisons': comparisons, 'scope': run['scope'], 'binaries': run.get('binaries'),
               'fuse_cpu_source': 'saved before/after.prom process_cpu_seconds_total; raw result.json may contain supervisor CPU'}
    (root/'summary.json').write_text(json.dumps(summary, indent=2)+'\n')
    lines = ['# JuiceFS Overlay 热读开销', '',
             '单层对照与 Overlay 使用同一 forkredis 引擎、同一个已封存 lower 和同一二进制。单层只读；Overlay 保持可写、文件内容无修改。原生 Redis JuiceFS 单独列出，用于区分新引擎成本。', '',
             '| 版本 | 元数据缓存 | 负载 | 单层 ms | Overlay ms | 配对开销中位数 | 各轮开销范围 | 原生 Redis ms |',
             '|---|---|---|---:|---:|---:|---:|---:|']
    for group in groups:
        arms = group['arms']
        overhead = group['paired_overhead_percent']
        lines.append(f"| {group['variant']} | {group['profile']} | {group['fixture']} | {arms['lower']['wall_ms']:.2f} | {arms['overlay']['wall_ms']:.2f} | {group['median_overhead_percent']:+.1f}% | {min(overhead):.1f}%–{max(overhead):.1f}% | {arms['native']['wall_ms']:.2f} |")
    if comparisons:
        lines += ['', '## 新旧二进制配对比较', '',
                  '| 缓存档 | 负载 | 对照 | 优化前 ms | 优化后 ms | 耗时减少（配对中位数） |',
                  '|---|---|---|---:|---:|---:|']
        for row in comparisons:
            lines.append(f"| {row['profile']} | {row['fixture']} | {row['arm']} | {row['before_ms']:.2f} | {row['after_ms']:.2f} | {row['median_time_reduction_percent']:.1f}% |")
    lines += ['', '表中耗时和配对开销均取中位数。每一对先算 Overlay/单层，再汇总；相对原生 Redis 的比值另存于 summary.json。负载固定，没有模型调用。', '',
              'default：属性和目录项缓存 1 秒。diagnostic：这三项缓存为 0。两组均禁用 open-cache、预读和预取，数据缓存固定 128 MiB；O_DIRECT 绕过内核数据页缓存，仍使用 JuiceFS 数据缓存。因此这里不是默认完整挂载配置的吞吐测试。', '',
              '全部计时区间对象 GET 与对象读取字节为 0，FUSE 读取次数和字节数与负载逐项相符。Redis 命令统计包含 Lua 内部命令，不能解释成网络往返次数。对象后端为本机 file://，Redis 也在本机；跨机 Redis、冷数据和带上层修改的性能不由本次结果覆盖。', '',
              '客户端 CPU 从已保存的 before/after.prom 的 process_cpu_seconds_total 计算。早期采集脚本在 result.json/process.json 中记录的是前台监督进程 CPU，不能代表实际 FUSE worker；原始文件保留，汇总已改用 worker 的指标。', '',
              f"源码：`{run['source_commit']}`；二进制 SHA-256：`{run['binary_sha256']}`。", '',
              '[逐次汇总](cell-summary.csv) · [分组及配对数据](summary.json) · [完整命令和环境](run.json)', '']
    (root/'RESULTS.zh.md').write_text('\n'.join(lines))
    return summary


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    report = summarize(args.directory)
    print(json.dumps({'cells': report['cells'], 'groups': [
        {key: group[key] for key in ['variant', 'profile', 'fixture', 'pairs', 'median_overhead_percent']}
        for group in report['groups']]}, indent=2))
