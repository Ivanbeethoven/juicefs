#!/usr/bin/env python3
"""Compare hot reads on sealed lower, writable fork, and native Redis JuiceFS.

Linux only. Starts its own loopback Redis; never flushes an existing database or
host caches. All mounts/processes live in a new output directory. Object storage
is local file storage, excluded from measured reads by a checked warm-up gate.
"""
import argparse
import gzip
import hashlib
import itertools
import json
import os
from pathlib import Path
import platform
import re
import socket
import subprocess
import time
import traceback
from urllib.request import urlopen
import uuid

from lazy_clone_smoke import Redis


def save(path, value):
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + '\n')


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


def metrics(raw):
    result = {}
    for line in raw.splitlines():
        match = re.fullmatch(r'([a-zA-Z_:][a-zA-Z0-9_:]*(?:\{[^}]*\})?) ([0-9.eE+-]+)', line)
        if match:
            result[match[1]] = float(match[2])
    return result


def total(values, suffix, method=None):
    return sum(value for key, value in values.items()
               if key.split('{')[0].endswith(suffix)
               and (method is None or f'method="{method}"' in key))


def difference(after, before):
    return {key: value - before.get(key, 0) for key, value in after.items()}


def command_counts(raw):
    return {name: int(count) for name, count in re.findall(r'cmdstat_(.*):calls=(\d+),', raw)}


def proc_sample(pid):
    directory = Path('/proc') / str(pid)
    result = {}
    for name in ['stat', 'status', 'io']:
        result[name] = (directory / name).read_text()
    fields = result['stat'].split(') ', 1)[1].split()
    result['cpu_ticks'] = int(fields[11]) + int(fields[12])
    return result


def host_sample():
    result = {'unix_ns': time.time_ns()}
    for name in ['stat', 'loadavg', 'meminfo', 'pressure/cpu', 'pressure/io', 'pressure/memory']:
        path = Path('/proc') / name
        if path.exists():
            result[name] = path.read_text()
    return result


def fixture_specs(quick):
    if quick:
        return [('tiny2', [13, 64], 64, 128)]
    return [('tiny2', [13, 64], 64, 512),
            ('small128', [4096] * 128, 4096, 16),
            ('blocks8', [1024*1024] * 8, 65536, 256)]


def read_round(point, files, block, iteration, capture=True):
    record = {'round': iteration, 'open_ns': [], 'pread_ns': [], 'close_ns': [], 'bytes': 0}
    started = time.monotonic_ns()
    directory = os.open(point, os.O_RDONLY | os.O_DIRECTORY)
    try:
        names = os.listdir(directory)
    finally:
        os.close(directory)
    record['open_list_ns'] = time.monotonic_ns() - started
    assert sorted(names) == sorted(files)
    started = time.monotonic_ns()
    first = next(iter(files))
    stat = os.stat(point / first, follow_symlinks=False)
    record['stat_ns'] = time.monotonic_ns() - started
    assert stat.st_size == len(files[first])
    for index, (name, content) in enumerate(files.items()):
        size = min(block, len(content))
        offset = ((iteration * 17 + index * 3) % max(1, len(content)//size)) * size
        expected = content[offset:offset+size]
        started = time.monotonic_ns()
        fd = os.open(point / name, os.O_RDONLY | os.O_NOFOLLOW | os.O_DIRECT)
        record['open_ns'].append(time.monotonic_ns()-started)
        try:
            started = time.monotonic_ns()
            data = os.pread(fd, size, offset)
            record['pread_ns'].append(time.monotonic_ns()-started)
            assert data == expected, name
            record['bytes'] += len(data)
        finally:
            started = time.monotonic_ns()
            os.close(fd)
            record['close_ns'].append(time.monotonic_ns()-started)
    return record if capture else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--redis-server', default='redis-server')
    parser.add_argument('--pairs', type=int, default=3)
    parser.add_argument('--client-cpus', default='8-11')
    parser.add_argument('--redis-cpu', type=int, default=12)
    parser.add_argument('--driver-cpu', type=int, default=13)
    parser.add_argument('--quick', action='store_true')
    args = parser.parse_args()
    assert args.pairs > 0 and Path('/dev/fuse').exists()
    binary, root = args.binary.resolve(), args.out.resolve()
    root.mkdir(parents=True, exist_ok=False)
    assert args.driver_cpu in os.sched_getaffinity(0)
    os.sched_setaffinity(0, {args.driver_cpu})
    token = 'read-' + uuid.uuid4().hex[:12]
    result = {'passed': False, 'source_commit': args.source_commit, 'binary_sha256': sha(binary),
              'script_sha256': sha(Path(__file__)), 'uname': list(platform.uname()),
              'parameters': {k: str(v) if isinstance(v, Path) else v for k, v in vars(args).items()},
              'commands': [], 'cells': [], 'host_before': host_sample(),
              'scope': 'hot local file object store; local Redis; O_DIRECT at kernel; writable empty upper',
              'mounts_closed': False, 'redis_stopped': False}
    save(root/'run.json', result)
    redis_process = None
    redis = None
    mounts = {}
    environment = dict(os.environ, GOMAXPROCS='4')

    def run(*argv, check=True):
        done = subprocess.run(list(map(str, argv)), env=environment, capture_output=True, text=True, timeout=120)
        result['commands'].append({'argv': list(map(str, argv)), 'returncode': done.returncode,
                                   'stdout': done.stdout, 'stderr': done.stderr})
        if check and done.returncode:
            raise RuntimeError(f'{argv}: {done.stderr}')
        return done

    def scrape(port):
        with urlopen(f'http://127.0.0.1:{port}/metrics', timeout=15) as response:
            raw = response.read().decode()
        values = metrics(raw)
        assert any('fuse_ops_' in key for key in values), 'missing client metrics'
        return raw, values

    def mount(uri, label, profile, readonly=False):
        point = root/'mounts'/label
        point.mkdir(parents=True, exist_ok=False)
        log = (root/(label+'.log')).open('xb')
        port = free_port()
        ttl = '1s' if profile == 'default' else '0s'
        argv = ['taskset', '-c', args.client_cpus, str(binary), 'mount',
                '--no-usage-report', '--no-bgjob', '--backup-meta', '0', '--max-deletes', '0',
                '--cache-dir', 'memory', '--cache-size', '128', '--buffer-size', '64',
                '--max-readahead', '0', '--prefetch', '0', '--open-cache', '0s',
                '--attr-cache', ttl, '--entry-cache', ttl, '--dir-entry-cache', ttl,
                '--negative-entry-cache', '0s', '--atime-mode', 'noatime', '--no-syslog',
                '--metrics', f'127.0.0.1:{port}']
        if readonly:
            argv.append('--read-only')
        argv += [uri, str(point)]
        process = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, env=environment)
        mounts[point] = (process, log, port)
        result['commands'].append({'argv': argv, 'pid': process.pid})
        deadline = time.monotonic()+30
        while True:
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError(f'mount failed: {label}')
            try:
                if os.path.ismount(point):
                    scrape(port)
                    break
            except OSError:
                pass
            time.sleep(.05)
        return point, process, port

    def unmount(point):
        process, log, _ = mounts[point]
        run(binary, 'umount', point)
        assert process.wait(timeout=30) == 0 and not os.path.ismount(point)
        log.close()
        del mounts[point]

    try:
        redis_port = free_port()
        redis_dir = root/'redis'
        redis_dir.mkdir()
        redis_args = ['taskset', '-c', str(args.redis_cpu), args.redis_server,
                      '--bind', '127.0.0.1', '--port', str(redis_port), '--protected-mode', 'yes',
                      '--save', '', '--appendonly', 'no', '--maxmemory', '256mb',
                      '--maxmemory-policy', 'noeviction', '--dir', str(redis_dir),
                      '--logfile', str(root/'redis.log')]
        redis_process = subprocess.Popen(redis_args, stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
        result['commands'].append({'argv': redis_args, 'pid': redis_process.pid})
        deadline = time.monotonic()+15
        while redis is None:
            try:
                redis = Redis(f'127.0.0.1:{redis_port}')
                assert redis.command('PING') == 'PONG'
            except OSError:
                if redis_process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('owned Redis did not start')
                time.sleep(.05)
        result['redis_info'] = redis.command('INFO', 'server')
        lower_uri = f'forkredis://127.0.0.1:{redis_port}/0?volume={token}&branch=main'
        native_uri = f'redis://127.0.0.1:{redis_port}/1'
        specs = fixture_specs(args.quick)
        fixture = {}
        for name, sizes, block, rounds in specs:
            fixture[name] = {f'f{index:04d}': hashlib.shake_256(f'{name}/{index}'.encode()).digest(size)
                             for index, size in enumerate(sizes)}
        result['files'] = {name: {path: {'bytes': len(content), 'sha256': hashlib.sha256(content).hexdigest()}
                                   for path, content in files.items()} for name, files in fixture.items()}
        for arm, uri in [('lower', lower_uri), ('native', native_uri)]:
            if arm == 'native':
                redis.command('SELECT', 1)
                assert redis.command('DBSIZE') == 0, 'refuse to format nonempty native database'
                redis.command('SELECT', 0)
            run(binary, 'format', '--storage', 'file', '--bucket', root/('objects-'+arm),
                '--trash-days', '0', uri, token+'-'+arm)
            point, _, _ = mount(uri, 'seed-'+arm, 'diagnostic')
            for name, files in fixture.items():
                directory = point/name
                directory.mkdir()
                for filename, content in files.items():
                    with (directory/filename).open('wb') as stream:
                        stream.write(content)
                        stream.flush()
                        os.fsync(stream.fileno())
            unmount(point)
        run(binary, 'clone', '--seal', lower_uri)
        child_uri = run(binary, 'clone', '--lazy', lower_uri, 'child').stdout.strip()
        assert child_uri.startswith('forkredis://')
        uris = {'lower': lower_uri, 'overlay': child_uri, 'native': native_uri}
        profiles = ['diagnostic'] if args.quick else ['default', 'diagnostic']
        orders = list(itertools.permutations(['lower', 'overlay', 'native']))
        # First two pairs reverse the primary comparison; all six orders can be used.
        orders = [orders[i] for i in [0, 5, 2, 3, 1, 4]]
        for profile in profiles:
            for name, sizes, block, rounds in specs:
                files = fixture[name]
                for pair in range(args.pairs):
                    for arm in orders[pair % len(orders)]:
                        label = f'{profile}-{name}-p{pair+1}-{arm}'
                        directory = root/'cells'/label
                        directory.mkdir(parents=True)
                        point, process, port = mount(uris[arm], label, profile, readonly=(arm == 'native'))
                        config_path = point/'.jfs.config'
                        if not config_path.exists():
                            config_path = point/'.config'
                        config = json.loads(config_path.read_text())
                        save(directory/'mount-config.json', config)
                        worker_pid = int(config['Pid'])
                        if arm == 'overlay':
                            fd = os.open(point/name/next(iter(files)), os.O_WRONLY)
                            os.close(fd)  # prove the measured upper is writable, without modifying content
                        warming = []
                        for attempt in range(4):
                            raw_before, before = scrape(port)
                            for filename, content in files.items():
                                fd = os.open(point/name/filename, os.O_RDONLY | os.O_DIRECT)
                                try:
                                    assert os.pread(fd, len(content), 0) == content
                                finally:
                                    os.close(fd)
                            raw_after, after = scrape(port)
                            delta = difference(after, before)
                            row = {'attempt': attempt, 'object_gets': total(delta, 'object_request_durations_histogram_seconds_count', 'GET'),
                                   'object_get_bytes': total(delta, 'object_request_data_bytes', 'GET'),
                                   'fuse_read_bytes': total(delta, 'fuse_read_size_bytes_sum')}
                            warming.append(row)
                            save(directory/'warm.json', warming)
                            assert row['fuse_read_bytes'] == sum(map(len, files.values())), 'warm-up bypassed FUSE'
                            if attempt > 0 and row['object_gets'] == 0 and row['object_get_bytes'] == 0:
                                break
                        else:
                            raise AssertionError('could not establish hot data')
                        save(directory/'warm.json', warming)
                        # Prime exactly the measured metadata paths; data must remain hot.
                        read_round(point/name, files, block, 0, capture=False)
                        before_raw, before_metrics = scrape(port)
                        before_redis = redis.command('INFO', 'commandstats')
                        before_process = proc_sample(worker_pid)
                        before_redis_process = proc_sample(redis_process.pid)
                        before_host = host_sample()
                        started = time.monotonic_ns()
                        samples = [read_round(point/name, files, block, i) for i in range(rounds)]
                        elapsed = time.monotonic_ns()-started
                        after_process = proc_sample(worker_pid)
                        after_redis_process = proc_sample(redis_process.pid)
                        after_redis = redis.command('INFO', 'commandstats')
                        after_raw, after_metrics = scrape(port)
                        delta = difference(after_metrics, before_metrics)
                        read_bytes = sum(s['bytes'] for s in samples)
                        read_ops = rounds*len(files)
                        observed = {'object_gets': total(delta, 'object_request_durations_histogram_seconds_count', 'GET'),
                                    'object_get_bytes': total(delta, 'object_request_data_bytes', 'GET'),
                                    'fuse_read_ops': total(delta, 'fuse_read_size_bytes_count'),
                                    'fuse_read_bytes': total(delta, 'fuse_read_size_bytes_sum'),
                                    'fuse_write_ops': total(delta, 'fuse_written_size_bytes_count'),
                                    'fuse_write_bytes': total(delta, 'fuse_written_size_bytes_sum'),
                                    'blockcache_hits': total(delta, 'blockcache_hits'),
                                    'blockcache_hit_bytes': total(delta, 'blockcache_hit_bytes'),
                                    'blockcache_misses': total(delta, 'blockcache_miss'),
                                    'meta_ops': total(delta, 'meta_ops_durations_histogram_seconds_count')}
                        valid = (observed['object_gets'] == observed['object_get_bytes'] == 0
                                 and observed['fuse_read_ops'] == read_ops
                                 and observed['fuse_read_bytes'] == read_bytes)
                        redis_delta = difference(command_counts(after_redis), command_counts(before_redis))
                        cell = {'label': label, 'arm': arm, 'profile': profile, 'fixture': name, 'pair': pair, 'valid': valid,
                                'wall_ns': elapsed, 'read_ops': read_ops, 'read_bytes': read_bytes,
                                'observed': observed, 'redis_commands': redis_delta,
                                'fuse_cpu_seconds': (after_process['cpu_ticks']-before_process['cpu_ticks'])/os.sysconf('SC_CLK_TCK'),
                                'redis_cpu_seconds': (after_redis_process['cpu_ticks']-before_redis_process['cpu_ticks'])/os.sysconf('SC_CLK_TCK'),
                                'timing_ns': {key: sum(sum(s[key]) if isinstance(s[key], list) else s[key] for s in samples)
                                              for key in ['open_list_ns', 'stat_ns', 'open_ns', 'pread_ns', 'close_ns']}}
                        save(directory/'result.json', cell)
                        save(directory/'process.json', {'before': before_process, 'after': after_process,
                                                       'redis_before': before_redis_process, 'redis_after': after_redis_process})
                        save(directory/'host.json', {'before': before_host, 'after': host_sample()})
                        (directory/'before.prom').write_text(before_raw)
                        (directory/'after.prom').write_text(after_raw)
                        (directory/'redis-before.txt').write_text(before_redis)
                        (directory/'redis-after.txt').write_text(after_redis)
                        with gzip.open(directory/'samples.ndjson.gz', 'wt', compresslevel=3) as stream:
                            for row in samples:
                                stream.write(json.dumps(row, separators=(',', ':'))+'\n')
                        result['cells'].append(cell)
                        assert valid, (observed, read_ops, read_bytes)
                        unmount(point)
                        save(root/'run.json', result)
                        print(json.dumps({'label': label, 'wall_ms': elapsed/1e6, 'object_gets': observed['object_gets'],
                                          'redis_zrange': redis_delta.get('zrevrangebyscore', 0)}), flush=True)
        result['passed'] = True
    except BaseException:
        result['error'] = traceback.format_exc()
        raise
    finally:
        errors = []
        for point in list(mounts):
            try:
                unmount(point)
            except Exception as error:
                errors.append(str(error))
                process, log, _ = mounts[point]
                process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    errors.append(f'mount process {process.pid} did not stop')
                log.close()
        result['mounts_closed'] = not any(os.path.ismount(p) for p in (root/'mounts').glob('*'))
        if redis:
            try:
                redis.command('SAVE')
            except Exception as error:
                errors.append(str(error))
            redis.close()
        if redis_process:
            redis_process.terminate()
            try:
                redis_process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                errors.append(f'Redis process {redis_process.pid} did not stop')
            result['redis_stopped'] = redis_process.poll() is not None
        if errors or not result['mounts_closed'] or not result['redis_stopped']:
            result['passed'] = False
            result['cleanup_errors'] = errors
        result['host_after'] = host_sample()
        save(root/'run.json', result)
    assert result['passed']


if __name__ == '__main__':
    main()
