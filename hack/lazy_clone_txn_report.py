#!/usr/bin/env python3
"""Extract forkredis transaction phases from saved paired FUSE runs (no rerun)."""
import argparse
import csv
import json
from pathlib import Path
import re
import statistics

from lazy_clone_read_report import summarize


PHASES = ('transaction', 'begin', 'body', 'data_io', 'end', 'commit',
          'cleanup', 'publish', 'cached_total', 'cached_prepare', 'cached_validate')


def counters(path):
    values = {}
    for line in path.read_text().splitlines():
        match = re.fullmatch(r'(\w+)(?:\{([^}]*)\})? ([0-9.eE+-]+)', line)
        if not match:
            continue
        name, labels, value = match.groups()
        if name.startswith('juicefs_forkredis_'):
            phase = re.search(r'(?:^|,)phase="([^"]+)"', labels or '')
            key = (name.removeprefix('juicefs_'), phase[1] if phase else '')
            if phase:
                assert key not in values, ('ambiguous phase counter', key)
            values[key] = values.get(key, 0) + float(value)
    return values


def commandstats(path):
    result = {}
    for name, fields in re.findall(r'^cmdstat_([^:]+):([^\r\n]+)', path.read_text(), re.M):
        for key, value in re.findall(r'(\w+)=([0-9.]+)', fields):
            result[(name, key)] = float(value)
    return result


def delta(after, before, key):
    value = after.get(key, 0) - before.get(key, 0)
    assert value >= -1e-9, ('counter reset', key, value)
    return max(0, value)


def report(root):
    overview = summarize(root)
    run = json.loads((root / 'run.json').read_text())
    rows = []
    for cell in run['cells']:
        directory = root / 'cells' / cell['label']
        before, after = [counters(directory / name) for name in ('before.prom', 'after.prom')]
        row = {key: cell[key] for key in ('label', 'variant', 'profile', 'fixture', 'arm', 'pair')}
        row['wall_ms'] = cell['wall_ns'] / 1e6
        row['instrumented'] = ('forkredis_phase_calls_total', 'transaction') in after
        for phase in PHASES:
            row[phase + '_ms'] = 1000 * delta(after, before, ('forkredis_phase_seconds_total', phase))
            row[phase + '_calls'] = delta(after, before, ('forkredis_phase_calls_total', phase))
        for key in ('cached_read_hits', 'cached_read_fallbacks', 'cache_hits', 'cache_misses', 'read_batches'):
            row[key] = delta(after, before, ('forkredis_' + key + '_total', ''))
        row['body_local_ms'] = row['body_ms'] - row['data_io_ms']
        row['pinned_other_ms'] = row['transaction_ms'] - sum(row[p + '_ms'] for p in
                                                          ('begin', 'body', 'end', 'commit', 'cleanup', 'publish'))
        # Counts client Redis API executions/pipelines, not wire-level retries.
        row['redis_api_calls'] = sum(row[p + '_calls'] for p in
                                    ('begin', 'data_io', 'end', 'commit', 'cleanup', 'cached_validate'))
        rb, ra = [commandstats(directory / name) for name in ('redis-before.txt', 'redis-after.txt')]
        for command in ('evalsha', 'eval', 'hmget', 'zrevrangebyscore', 'zrangebylex'):
            for key in ('calls', 'usec', 'failed_calls', 'rejected_calls'):
                row['redis_' + command + '_' + key] = delta(ra, rb, (command, key))
        rows.append(row)
    with (root / 'txn-cells.csv').open('w', newline='') as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    groups = []
    keys = ('variant', 'profile', 'fixture', 'arm')
    for group in sorted({tuple(row[k] for k in keys) for row in rows}):
        selected = [r for r in rows if tuple(r[k] for k in keys) == group]
        result = dict(zip(keys, group))
        result['cells'] = len(selected)
        result['instrumented'] = all(r['instrumented'] for r in selected)
        result['median'] = {k: statistics.median(r[k] for r in selected) for k in selected[0]
                            if k not in (*keys, 'label', 'pair', 'instrumented')}
        groups.append(result)
    output = {'cells': len(rows), 'binaries': overview['binaries'], 'groups': groups,
              'units': 'client elapsed milliseconds and API execution counts; not exclusive wall-clock phases',
              'nested': 'transaction includes begin/body/end/commit/cleanup/publish; body includes data_io; cached_total includes cached_prepare/cached_validate',
              'redis_usec': 'Redis execution time; Lua includes internal commands, so do not add Lua and nested command usec',
              'unavailable': 'instrumented=false means phase counters are unavailable, not measured zero'}
    (root / 'txn-summary.json').write_text(json.dumps(output, indent=2) + '\n')
    lines = ['# forkredis 事务分段计时', '',
             '| 版本 | 缓存档 | 负载 | 对照 | 总耗时 ms | 开始 ms | 查询 ms | 结束 ms | 提交 ms | 新校验 ms | Redis API 次数 |',
             '|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|']
    for group in groups:
        if not group['instrumented']:
            continue
        m = group['median']
        lines.append('| ' + ' | '.join(str(group[k]) for k in keys) + ' | ' +
                     ' | '.join(f'{m[k]:.2f}' for k in ('wall_ms', 'begin_ms', 'data_io_ms', 'end_ms',
                                                       'commit_ms', 'cached_validate_ms', 'redis_api_calls')) + ' |')
    lines += ['', '每组为各次测量的中位数。分段为客户端累计耗时，包括连接等待、网络和 Redis 执行；并发操作不能直接相加解释墙钟耗时。body 包含查询，transaction 包含完整事务各段。', '',
              'Redis API 次数为客户端发起的命令或 pipeline 次数，不包含重试产生的额外网络发送。Redis commandstats 包含 Lua 内部命令；其 calls 不能当成网络往返，Lua 与内部命令的 usec 不能重复相加。', '',
              '原始逐次操作、Prometheus 文本和 Redis commandstats 留在 cells/。无计时指标的二进制标记 instrumented=false，不记作耗时为零。', '']
    (root / 'TRANSACTIONS.zh.md').write_text('\n'.join(lines))
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    result = report(args.directory)
    print(json.dumps({'cells': result['cells'], 'groups': result['groups']}, indent=2))
