#!/usr/bin/env python3
"""Exercise sealed lower + two writable JuiceFS mounts on an isolated Redis.

Uses local file object storage. The output directory must not exist. It retains
commands, logs, hashes and data on failure; only its own mounts are stopped.
"""
import argparse
import errno
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import traceback
import uuid


class Redis:
    def __init__(self, address):
        host, port = address.rsplit(':', 1)
        self.socket = socket.create_connection((host, int(port)), timeout=5)
        self.stream = self.socket.makefile('rb')

    def command(self, *arguments):
        args = [str(a).encode() for a in arguments]
        data = b'*%d\r\n' % len(args)
        for arg in args:
            data += b'$%d\r\n' % len(arg) + arg + b'\r\n'
        self.socket.sendall(data)
        return self.read()

    def read(self):
        line = self.stream.readline()
        if not line:
            raise RuntimeError('Redis connection closed')
        kind, value = line[:1], line[1:-2]
        if kind == b'+': return value.decode()
        if kind == b'-': raise RuntimeError(value.decode())
        if kind == b':': return int(value)
        if kind == b'$':
            n = int(value)
            if n == -1: return None
            result = self.stream.read(n)
            assert self.stream.read(2) == b'\r\n'
            return result.decode()
        if kind == b'*': return [self.read() for _ in range(int(value))]
        raise RuntimeError('Invalid Redis response')

    def close(self):
        self.stream.close()
        self.socket.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--redis', required=True, help='isolated, unauthenticated host:port')
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    binary, root = args.binary.resolve(), args.out.resolve()
    assert binary.is_file()
    root.mkdir(parents=True, exist_ok=False)
    owner = 'fork-' + uuid.uuid4().hex[:12]
    uri = f'forkredis://{args.redis}/0?volume={owner}&branch=main'
    prefix = '{jfs-fork:' + owner + '}:'
    result = dict(passed=False, volume=owner, source_url=uri,
                  binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                  commands=[], forks=[], mounts_closed=False)
    processes = {}
    environment = dict(os.environ, GOMAXPROCS='4')
    redis = Redis(args.redis)

    def run(*argv, check=True):
        started = time.monotonic_ns()
        done = subprocess.run([str(binary), *map(str, argv)], capture_output=True,
                              text=True, timeout=90, env=environment)
        result['commands'].append(dict(argv=list(map(str, argv)), returncode=done.returncode,
            elapsed_ns=time.monotonic_ns()-started, stdout=done.stdout, stderr=done.stderr))
        if check and done.returncode:
            raise RuntimeError(f'{argv}: {done.stderr}')
        return done

    def mount(url, label):
        point = root / label
        point.mkdir(exist_ok=True)
        log = (root / f'{label}-{time.time_ns()}.log').open('xb')
        argv = [str(binary), 'mount', '--no-usage-report', '--no-bgjob', '--backup-meta', '0',
                '--cache-dir', 'memory', '--cache-size', '64', '--attr-cache', '0',
                '--entry-cache', '0', '--dir-entry-cache', '0', '--no-syslog',
                '--enable-xattr', '--metrics', '127.0.0.1:0', url, str(point)]
        process = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, env=environment)
        processes[point] = (process, log)
        result['commands'].append(dict(argv=argv[1:], pid=process.pid))
        deadline = time.monotonic() + 30
        while not os.path.ismount(point):
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError(f'mount failed: {label}; see mount log')
            time.sleep(.05)
        return point

    def unmount(point):
        process, log = processes[point]
        run('umount', point)
        code = process.wait(timeout=15)
        log.close()
        assert code == 0 and not os.path.ismount(point), 'unclean unmount'
        del processes[point]

    def write(path, data):
        with path.open('wb') as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())

    try:
        run('format', '--storage', 'file', '--bucket', root / 'objects', '--trash-days', '0', uri, owner)
        source = mount(uri, 'source')
        (source / 'nested').mkdir()
        original = bytes(range(256)) * 512
        write(source / 'nested/file', original)
        os.link(source / 'nested/file', source / 'hard')
        os.symlink('nested/file', source / 'sym')
        os.setxattr(source / 'hard', 'user.fork', b'lower')
        busy = run('clone', '--seal', uri, check=False)
        assert busy.returncode and 'unmount' in busy.stderr
        unmount(source)
        run('clone', '--seal', uri)
        children = []
        for name in ['child1', 'child2']:
            before = set(redis.command('KEYS', prefix + '*'))
            stats_before = redis.command('INFO', 'commandstats')
            started = time.monotonic_ns()
            done = run('clone', '--lazy', uri, name)
            elapsed = time.monotonic_ns() - started
            stats_after = redis.command('INFO', 'commandstats')
            after = set(redis.command('KEYS', prefix + '*'))
            assert after - before == {prefix + 'head:' + name}
            assert redis.command('ZCARD', prefix + 'index:' + name) == 0
            child_uri = done.stdout.strip()
            assert child_uri.startswith('forkredis://')
            children.append(child_uri)
            result['forks'].append(dict(name=name, wall_ns=elapsed, added_keys=sorted(after-before),
                upper_records=0, redis_before=stats_before, redis_after=stats_after))
        source = mount(uri, 'source')
        one, two = mount(children[0], 'child1'), mount(children[1], 'child2')
        try:
            descriptor = os.open(source / 'hard', os.O_WRONLY)
        except OSError as error:
            assert error.errno == errno.EROFS
        else:
            os.close(descriptor)
            raise AssertionError('sealed lower allowed a write open')
        assert (one / 'hard').read_bytes() == original
        assert (two / 'hard').read_bytes() == original
        write(one / 'nested/file', b'child')
        with (one / 'hard').open('r+b') as f:
            f.truncate(4096)
            os.fsync(f.fileno())
        changed = b'child' + bytes(4091)
        assert (one / 'hard').read_bytes() == changed
        os.setxattr(one / 'hard', 'user.fork', b'upper')
        (one / 'nested/file').unlink()
        (one / 'sym').unlink()
        write(one / 'nested/new', b'new child file')
        (one / 'nested').rename(one / 'moved')
        for point in [source, two]:
            assert (point / 'nested/file').read_bytes() == original
            assert (point / 'hard').read_bytes() == original
            assert os.getxattr(point / 'hard', 'user.fork') == b'lower'
            assert os.readlink(point / 'sym') == 'nested/file'
            assert not (point / 'moved').exists()
        unmount(one)
        one = mount(children[0], 'child1')
        assert (one / 'hard').read_bytes() == changed
        assert (one / 'moved/new').read_bytes() == b'new child file'
        assert os.getxattr(one / 'hard', 'user.fork') == b'upper'
        assert not (one / 'moved/file').exists()
        assert not (one / 'nested').exists()
        assert not (one / 'sym').is_symlink()
        for argv in [('gc', '--delete', children[0]), ('destroy', '--force', children[0], 'not-a-real-uuid')]:
            rejected = run(*argv, check=False)
            assert rejected.returncode and 'unsupported for forkredis' in rejected.stderr
        assert (source / 'hard').read_bytes() == original
        result.update(passed=True, source_sha256=hashlib.sha256(original).hexdigest(),
                      child_sha256=hashlib.sha256(changed).hexdigest(),
                      checks=['readonly lower', 'no metadata copy at fork', 'siblings isolated',
                              'write', 'truncate and holes', 'unlink', 'rename', 'hardlink',
                              'symlink', 'xattr', 'fresh process remount', 'gc/destroy refused'])
    except BaseException:
        result['error'] = traceback.format_exc()
        raise
    finally:
        errors = []
        for point in list(processes):
            try:
                unmount(point)
            except Exception as error:
                errors.append(str(error))
                process, log = processes[point]
                process.terminate()
                try: process.wait(timeout=10)
                except subprocess.TimeoutExpired: errors.append(f'process {process.pid} still alive')
                log.close()
        result['mounts_closed'] = not any(os.path.ismount(root / label) for label in ['source', 'child1', 'child2'])
        if errors or not result['mounts_closed']:
            result['passed'] = False
            result['cleanup_errors'] = errors
        (root / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
        redis.close()
    assert result['passed']
    print(json.dumps({k:result[k] for k in ['passed','forks','mounts_closed','checks']}, indent=2))


if __name__ == '__main__':
    main()
