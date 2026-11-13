/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package meta

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

const forkMaxDepth = 64
const forkMaxRevision = 1<<53 - 2 // Redis scores must exactly represent revisions.
const forkTransactionTTL = time.Minute

var forkName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type forkLayer struct {
	name     string
	revision uint64
	sealed   bool
	parent   string
	depth    int
}

// forkRedisClient stores immutable record versions so a transaction's reads
// cannot mix revisions. A fork adds a root reference, never a copy of records.
// Live read snapshots pin versions; commits prune older, unneeded versions.
// Shared layer/object collection is deliberately unsupported in this prototype.
type forkRedisClient struct {
	rdb     *redis.Client
	prefix  string
	branch  string
	parents []forkLayer
}

func (c *forkRedisClient) head(name string) string  { return c.prefix + "head:" + name }
func (c *forkRedisClient) index(name string) string { return c.prefix + "index:" + name }
func (c *forkRedisClient) readers() string          { return c.prefix + "readers:" + c.branch }
func (c *forkRedisClient) readerExpiry() string     { return c.prefix + "reader-expiry:" + c.branch }
func (c *forkRedisClient) record(name string, key []byte) string {
	return c.prefix + "record:" + name + ":" + hex.EncodeToString(key)
}

func parseForkURL(raw string) (*url.URL, *redis.Options, string, string, error) {
	if !strings.Contains(raw, "://") {
		raw = "forkredis://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, "", "", err
	}
	if u.Scheme != "forkredis" {
		return nil, nil, "", "", fmt.Errorf("expected forkredis:// URL")
	}
	q := u.Query()
	volume, branch := q.Get("volume"), q.Get("branch")
	if branch == "" {
		branch = "main"
	}
	if !forkName.MatchString(volume) || !forkName.MatchString(branch) {
		return nil, nil, "", "", fmt.Errorf("volume and branch must be 1-64 letters, digits, underscores or hyphens")
	}
	original := *u
	q.Del("volume")
	q.Del("branch")
	u.Scheme, u.RawQuery = "redis", q.Encode()
	options, err := redis.ParseURL(u.String())
	if err != nil {
		return nil, nil, "", "", err
	}
	if options.Password == "" {
		options.Password = os.Getenv("META_PASSWORD")
	}
	return &original, options, volume, branch, nil
}

func newForkRedisClient(addr string) (*forkRedisClient, error) {
	_, options, volume, branch, err := parseForkURL(addr)
	if err != nil {
		return nil, err
	}
	c := &forkRedisClient{rdb: redis.NewClient(options), prefix: "{jfs-fork:" + volume + "}:", branch: branch}
	ctx := context.Background()
	if branch == "main" {
		// Only the initial volume can be initialized implicitly. A misspelled
		// child URL must fail instead of creating a new, empty filesystem.
		err = c.rdb.Eval(ctx, `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('HSET', KEYS[1], 'schema', '1', 'revision', '0', 'sealed', '0', 'parent', '', 'depth', '1', 'children', '0')
end
return 1`, []string{c.head(branch)}).Err()
		if err != nil {
			_ = c.rdb.Close()
			return nil, err
		}
	}
	layer, err := c.readHead(ctx, branch)
	seen := map[string]bool{branch: true}
	for err == nil && layer.parent != "" {
		if seen[layer.parent] || len(seen) >= forkMaxDepth {
			err = fmt.Errorf("invalid or excessive fork ancestry")
			break
		}
		seen[layer.parent] = true
		parent, e := c.readHead(ctx, layer.parent)
		if e != nil {
			err = e
			break
		}
		if !parent.sealed || parent.depth+1 != layer.depth {
			err = fmt.Errorf("fork lower must be sealed and have a valid depth")
			break
		}
		c.parents = append(c.parents, parent)
		layer = parent
	}
	if err != nil {
		_ = c.rdb.Close()
		return nil, err
	}
	return c, nil
}

func (c *forkRedisClient) readHead(ctx context.Context, name string) (forkLayer, error) {
	h, err := c.rdb.HGetAll(ctx, c.head(name)).Result()
	if err != nil {
		return forkLayer{}, err
	}
	if len(h) == 0 {
		return forkLayer{}, syscall.ENOENT
	}
	rev, e1 := strconv.ParseUint(h["revision"], 10, 64)
	depth, e2 := strconv.Atoi(h["depth"])
	if h["schema"] != "1" || e1 != nil || e2 != nil || rev > forkMaxRevision || depth < 1 || depth > forkMaxDepth || (h["sealed"] != "0" && h["sealed"] != "1") {
		return forkLayer{}, fmt.Errorf("unsupported or corrupt fork header")
	}
	return forkLayer{name: name, revision: rev, sealed: h["sealed"] == "1", parent: h["parent"], depth: depth}, nil
}

// These records describe a mount, not the persistent filesystem namespace.
func forkPrivateKey(key []byte) bool {
	return bytes.HasPrefix(key, []byte("F")) || bytes.HasPrefix(key, []byte("P")) ||
		bytes.HasPrefix(key, []byte("SE")) || bytes.HasPrefix(key, []byte("SH")) ||
		bytes.HasPrefix(key, []byte("SI")) || bytes.HasPrefix(key, []byte("SS")) ||
		bytes.Equal(key, []byte("CnextSession"))
}

type forkRedisTxn struct {
	c        *forkRedisClient
	ctx      context.Context
	layers   []forkLayer
	buffer   map[string][]byte
	readOnly bool
}

func (tx *forkRedisTxn) id() uint64 { return tx.layers[0].revision }

func (tx *forkRedisTxn) get(key []byte) []byte {
	if v, ok := tx.buffer[string(key)]; ok {
		return bytes.Clone(v)
	}
	if bytes.Equal(key, []byte("CnextChunk")) {
		n, err := tx.c.rdb.Get(tx.ctx, tx.c.prefix+"slice-id").Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			panic(err)
		}
		return packCounter(n)
	}
	for i, layer := range tx.layers {
		if i > 0 && forkPrivateKey(key) {
			break
		}
		values, err := tx.c.rdb.ZRevRangeByScore(tx.ctx, tx.c.record(layer.name, key), &redis.ZRangeBy{
			Min: "-inf", Max: strconv.FormatUint(layer.revision, 10), Offset: 0, Count: 1,
		}).Result()
		if err != nil {
			panic(err)
		}
		if len(values) == 0 {
			continue
		}
		b := []byte(values[0])
		if len(b) < 9 || binary.BigEndian.Uint64(b[:8]) > layer.revision || b[8] > 1 {
			panic(fmt.Errorf("corrupt fork record"))
		}
		if b[8] == 0 {
			return nil
		}
		value := make([]byte, len(b)-9)
		copy(value, b[9:])
		return value
	}
	return nil
}

func (tx *forkRedisTxn) gets(keys ...[]byte) [][]byte {
	values := make([][]byte, len(keys))
	for i, k := range keys {
		values[i] = tx.get(k)
	}
	return values
}

func (tx *forkRedisTxn) scan(begin, end []byte, keysOnly bool, handler func(k, v []byte) bool) {
	min, max := "-", "+"
	if len(begin) > 0 {
		min = "[" + string(begin)
	}
	if len(end) > 0 {
		max = "(" + string(end)
	}
	keys := make(map[string]bool)
	for i, layer := range tx.layers {
		names, err := tx.c.rdb.ZRangeByLex(tx.ctx, tx.c.index(layer.name), &redis.ZRangeBy{Min: min, Max: max}).Result()
		if err != nil {
			panic(err)
		}
		for _, k := range names {
			if i == 0 || !forkPrivateKey([]byte(k)) {
				keys[k] = true
			}
		}
	}
	inRange := func(k []byte) bool {
		return bytes.Compare(k, begin) >= 0 && (len(end) == 0 || bytes.Compare(k, end) < 0)
	}
	if inRange([]byte("CnextChunk")) {
		keys["CnextChunk"] = true
	}
	for k := range tx.buffer {
		if inRange([]byte(k)) {
			keys[k] = true
		}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		v := tx.get([]byte(k))
		if v == nil {
			continue
		}
		if keysOnly {
			v = nil
		}
		if !handler([]byte(k), v) {
			break
		}
	}
}

func (tx *forkRedisTxn) exist(prefix []byte) bool {
	found := false
	tx.scan(prefix, nextKey(prefix), true, func(k, v []byte) bool { found = true; return false })
	return found
}

func (tx *forkRedisTxn) set(key, value []byte) {
	if tx.readOnly || tx.layers[0].sealed {
		panic(syscall.EROFS)
	}
	if bytes.Equal(key, []byte("CnextChunk")) {
		panic(fmt.Errorf("slice IDs may only be reserved, never reset"))
	}
	if value == nil {
		value = []byte{}
	}
	tx.buffer[string(key)] = bytes.Clone(value)
}

func (tx *forkRedisTxn) append(key, value []byte) { tx.set(key, append(tx.get(key), value...)) }
func (tx *forkRedisTxn) incrBy(key []byte, value int64) int64 {
	if bytes.Equal(key, []byte("CnextChunk")) {
		if value == 0 {
			return parseCounter(tx.get(key))
		}
		if value < 0 || tx.readOnly || tx.layers[0].sealed {
			panic(syscall.EROFS)
		}
		// Reservation is global across siblings. Aborted transactions can leave
		// holes; allocated IDs must never be reused by another child.
		n, err := tx.c.rdb.IncrBy(tx.ctx, tx.c.prefix+"slice-id", value).Result()
		if err != nil {
			panic(err)
		}
		return n
	}
	n := parseCounter(tx.get(key)) + value
	if value != 0 {
		tx.set(key, packCounter(n))
	}
	return n
}
func (tx *forkRedisTxn) delete(key []byte) {
	if tx.readOnly || tx.layers[0].sealed {
		panic(syscall.EROFS)
	}
	if bytes.Equal(key, []byte("CnextChunk")) {
		panic(fmt.Errorf("cannot delete the global slice allocator"))
	}
	tx.buffer[string(key)] = nil
}

// Register the snapshot atomically with reading the head, before a commit can
// prune its versions. A crashed reader pins history for at most one minute.
var forkBegin = redis.NewScript(`
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local expired=redis.call('ZRANGEBYSCORE',KEYS[3],'-inf',now)
for _,token in ipairs(expired) do
  redis.call('ZREM',KEYS[2],token)
  redis.call('ZREM',KEYS[3],token)
end
local head=redis.call('HMGET',KEYS[1],'revision','sealed')
if not head[1] or not head[2] then return redis.error_reply('missing fork header') end
redis.call('ZADD',KEYS[2],head[1],ARGV[1])
redis.call('ZADD',KEYS[3],now+tonumber(ARGV[2]),ARGV[1])
return head`)

var forkEnd = redis.NewScript(`
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local expires=redis.call('ZSCORE',KEYS[2],ARGV[1])
redis.call('ZREM',KEYS[1],ARGV[1])
redis.call('ZREM',KEYS[2],ARGV[1])
if not expires or tonumber(expires)<=now then return 0 end
return 1`)

var forkCommit = redis.NewScript(`
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local expires=redis.call('ZSCORE',KEYS[4],ARGV[3])
if not expires or tonumber(expires)<=now then return -3 end
if redis.call('HGET', KEYS[1], 'sealed') ~= '0' then return -2 end
if redis.call('HGET', KEYS[1], 'revision') ~= ARGV[1] then return -1 end
local n=tonumber(ARGV[2])
for i=1,n do
  local t=redis.call('TYPE',KEYS[4+i]).ok
  if t~='none' and t~='zset' then return redis.error_reply('invalid fork record type') end
end
local t=redis.call('TYPE',KEYS[2]).ok
if t~='none' and t~='zset' then return redis.error_reply('invalid fork index type') end
local revision=tonumber(ARGV[1])+1
local readers=redis.call('ZRANGE',KEYS[3],0,0,'WITHSCORES')
local oldest=revision
if #readers>0 then oldest=tonumber(readers[2]) end
for i=1,n do
  local key=KEYS[4+i]
  redis.call('ZADD',key,revision,ARGV[3+i*2])
  redis.call('ZADD',KEYS[2],0,ARGV[2+i*2])
  local keep=redis.call('ZREVRANGEBYSCORE',key,oldest,'-inf','WITHSCORES','LIMIT',0,1)
  if #keep>0 then redis.call('ZREMRANGEBYSCORE',key,'-inf','('..keep[2]) end
end
redis.call('HSET',KEYS[1],'revision',revision)
return revision`)

func (c *forkRedisClient) transaction(ctx context.Context, f func(*kvTxn) error, retry int, readOnly bool) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				panic(p)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, forkTransactionTTL)
	defer cancel()
	token := rand.Text()
	values, err := forkBegin.Run(ctx, c.rdb, []string{c.head(c.branch), c.readers(), c.readerExpiry()}, token, forkTransactionTTL.Milliseconds()).StringSlice()
	if err != nil {
		return err
	}
	active := true
	defer func() {
		if active {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = forkEnd.Run(cleanup, c.rdb, []string{c.readers(), c.readerExpiry()}, token).Err()
		}
	}()
	if len(values) != 2 || (values[1] != "0" && values[1] != "1") {
		return fmt.Errorf("invalid fork snapshot")
	}
	revision, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil || revision > forkMaxRevision {
		return fmt.Errorf("invalid fork revision")
	}
	head := forkLayer{name: c.branch, revision: revision, sealed: values[1] == "1"}
	layers := append([]forkLayer{head}, c.parents...)
	tx := &forkRedisTxn{c: c, ctx: ctx, layers: layers, buffer: make(map[string][]byte), readOnly: readOnly}
	if err = f(&kvTxn{kvtxn: tx, retry: retry}); err != nil {
		return err
	}
	if len(tx.buffer) == 0 {
		valid, err := forkEnd.Run(ctx, c.rdb, []string{c.readers(), c.readerExpiry()}, token).Int()
		if err != nil {
			return err
		}
		active = false
		if valid == 0 {
			return fmt.Errorf("fork read snapshot expired: %w", context.DeadlineExceeded)
		}
		return nil
	}
	if head.revision >= forkMaxRevision {
		return fmt.Errorf("fork revision exhausted")
	}
	names := make([]string, 0, len(tx.buffer))
	for k := range tx.buffer {
		names = append(names, k)
	}
	sort.Strings(names)
	keys := []string{c.head(c.branch), c.index(c.branch), c.readers(), c.readerExpiry()}
	args := []interface{}{strconv.FormatUint(head.revision, 10), len(names), token}
	for _, k := range names {
		value := tx.buffer[k]
		record := make([]byte, 9+len(value))
		binary.BigEndian.PutUint64(record, head.revision+1)
		if value != nil {
			record[8] = 1
			copy(record[9:], value)
		}
		keys = append(keys, c.record(c.branch, []byte(k)))
		args = append(args, k, record)
	}
	result, err := forkCommit.Run(ctx, c.rdb, keys, args...).Int64()
	if err != nil {
		return err
	}
	if result == -1 {
		return redis.TxFailedErr
	}
	if result == -2 {
		return syscall.EROFS
	}
	if result == -3 {
		return fmt.Errorf("fork write snapshot expired: %w", context.DeadlineExceeded)
	}
	return nil
}

func (c *forkRedisClient) simpleTxn(ctx context.Context, f func(*kvTxn) error, retry int) error {
	return c.transaction(ctx, f, retry, true)
}
func (c *forkRedisClient) txn(ctx context.Context, f func(*kvTxn) error, retry int) error {
	return c.transaction(ctx, f, retry, false)
}
func (c *forkRedisClient) scan(prefix []byte, handler func(k, v []byte) bool) error {
	return c.simpleTxn(context.Background(), func(tx *kvTxn) error { tx.scan(prefix, nextKey(prefix), false, handler); return nil }, 0)
}
func (c *forkRedisClient) reset([]byte) error {
	return fmt.Errorf("fork volumes cannot be reset: shared layer reclamation is not implemented")
}
func (c *forkRedisClient) close() error               { return c.rdb.Close() }
func (c *forkRedisClient) shouldRetry(err error) bool { return errors.Is(err, redis.TxFailedErr) }
func (c *forkRedisClient) gc()                        {}
func (c *forkRedisClient) rewind(id uint64, factor int) uint64 {
	if id > uint64(factor) {
		return id - uint64(factor)
	}
	return 0
}
func (c *forkRedisClient) config(key string) interface{} { return nil }
func (c *forkRedisClient) name() string                  { return "forkredis" }

// SealWorkspace freezes an unmounted source. A concurrent metadata transaction
// either precedes the seal or fails its revision/readonly check.
func SealWorkspace(ctx context.Context, uri string) error {
	c, err := newForkRedisClient(uri)
	if err != nil {
		return err
	}
	defer c.close()
	head, err := c.readHead(ctx, c.branch)
	if err != nil {
		return err
	}
	if head.sealed {
		return nil
	}
	if head.revision >= forkMaxRevision {
		return fmt.Errorf("fork revision exhausted")
	}
	// No session, even an expired one, may be left behind: an expired mount can
	// still hold dirty data. Close/clean sessions before publishing a lower.
	tx := &forkRedisTxn{c: c, ctx: ctx, layers: append([]forkLayer{head}, c.parents...), buffer: make(map[string][]byte), readOnly: true}
	var snapshotErr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				if e, ok := p.(error); ok {
					snapshotErr = e
				} else {
					panic(p)
				}
			}
		}()
		if tx.get([]byte("setting")) == nil {
			snapshotErr = ErrNotFormatted
			return
		}
		for _, prefix := range []string{"SE", "SH", "SI", "SS"} {
			if tx.exist([]byte(prefix)) {
				snapshotErr = fmt.Errorf("unmount and close source sessions before sealing: %w", syscall.EBUSY)
				return
			}
		}
	}()
	if snapshotErr != nil {
		return snapshotErr
	}
	result, err := c.rdb.Eval(ctx, `
if redis.call('HGET',KEYS[1],'revision')~=ARGV[1] then return -1 end
if redis.call('HGET',KEYS[1],'sealed')~='0' then return -2 end
redis.call('HSET',KEYS[1],'sealed','1','revision',tonumber(ARGV[1])+1)
return 1`, []string{c.head(c.branch)}, strconv.FormatUint(head.revision, 10)).Int64()
	if err != nil {
		return err
	}
	if result == -1 {
		return redis.TxFailedErr
	}
	if result == -2 {
		return syscall.EROFS
	}
	return nil
}

// ForkWorkspace creates an empty upper referencing a sealed lower. No inode,
// directory, xattr, or chunk-index enumeration occurs here.
func ForkWorkspace(ctx context.Context, uri, name string) (string, error) {
	if !forkName.MatchString(name) {
		return "", fmt.Errorf("invalid child name")
	}
	u, _, _, _, err := parseForkURL(uri)
	if err != nil {
		return "", err
	}
	c, err := newForkRedisClient(uri)
	if err != nil {
		return "", err
	}
	defer c.close()
	result, err := c.rdb.Eval(ctx, `
if redis.call('HGET',KEYS[1],'schema')~='1' then return -4 end
if redis.call('HGET',KEYS[1],'sealed')~='1' then return -2 end
if redis.call('EXISTS',KEYS[2])~=0 then return -1 end
local depth=tonumber(redis.call('HGET',KEYS[1],'depth'))
if not depth or depth>=tonumber(ARGV[2]) then return -3 end
redis.call('HSET',KEYS[2],'schema','1','revision','0','sealed','0','parent',ARGV[1],'depth',depth+1,'children','0')
redis.call('HINCRBY',KEYS[1],'children',1)
return 1`, []string{c.head(c.branch), c.head(name)}, c.branch, forkMaxDepth).Int64()
	if err != nil {
		return "", err
	}
	switch result {
	case -1:
		return "", syscall.EEXIST
	case -2:
		return "", fmt.Errorf("seal the source before forking")
	case -3:
		return "", fmt.Errorf("maximum fork depth reached")
	case -4:
		return "", fmt.Errorf("unsupported fork schema")
	}
	q := u.Query()
	q.Set("branch", name)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
