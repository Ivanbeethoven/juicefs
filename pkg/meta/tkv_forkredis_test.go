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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func forkRedisFixture(t *testing.T) (*forkRedisClient, string) {
	t.Helper()
	address := os.Getenv("JFS_FORK_REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("requires an isolated JFS_FORK_REDIS_TEST_ADDR")
	}
	addr := fmt.Sprintf("%s/0?volume=t%d&branch=main", address, time.Now().UnixNano())
	c, err := newForkRedisClient(addr)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.close()) })
	return c, "forkredis://" + addr
}

func forkPut(t *testing.T, c *forkRedisClient, key, value string) {
	t.Helper()
	require.NoError(t, c.txn(context.Background(), func(tx *kvTxn) error {
		tx.set([]byte(key), []byte(value))
		return nil
	}, 0))
}

func forkGet(t *testing.T, c *forkRedisClient, key string) string {
	t.Helper()
	var value []byte
	require.NoError(t, c.simpleTxn(context.Background(), func(tx *kvTxn) error {
		value = tx.get([]byte(key))
		return nil
	}, 0))
	return string(value)
}

func TestForkRedisLayers(t *testing.T) {
	root, uri := forkRedisFixture(t)
	forkPut(t, root, "setting", `{"Name":"fork-test"}`)
	forkPut(t, root, "Aa", "lower")
	forkPut(t, root, "Ab", "hidden")
	_, err := ForkWorkspace(context.Background(), uri, "early")
	require.Error(t, err, "an unsealed source must not be forked")
	require.NoError(t, SealWorkspace(context.Background(), uri))
	childURI, err := ForkWorkspace(context.Background(), uri, "child")
	require.NoError(t, err)
	_, err = ForkWorkspace(context.Background(), uri, "child")
	require.ErrorIs(t, err, syscall.EEXIST)
	child, err := newForkRedisClient(strings.TrimPrefix(childURI, "forkredis://"))
	require.NoError(t, err)
	defer child.close()
	require.Equal(t, "lower", forkGet(t, child, "Aa"))
	forkPut(t, child, "Aa", "upper")
	require.NoError(t, child.txn(context.Background(), func(tx *kvTxn) error {
		tx.delete([]byte("Ab"))
		tx.set([]byte("Ac"), []byte("new"))
		return nil
	}, 0))
	require.Equal(t, "lower", forkGet(t, root, "Aa"))
	require.Equal(t, "hidden", forkGet(t, root, "Ab"))
	require.Empty(t, forkGet(t, child, "Ab"))
	var names []string
	require.NoError(t, child.scan([]byte("A"), func(k, v []byte) bool {
		names = append(names, string(k)+"="+string(v))
		return true
	}))
	require.Equal(t, []string{"Aa=upper", "Ac=new"}, names)
	require.ErrorIs(t, root.txn(context.Background(), func(tx *kvTxn) error {
		tx.set([]byte("Aa"), []byte("stale writer"))
		return nil
	}, 0), syscall.EROFS)
	require.NoError(t, SealWorkspace(context.Background(), childURI))
	grandURI, err := ForkWorkspace(context.Background(), childURI, "grand")
	require.NoError(t, err)
	grand, err := newForkRedisClient(strings.TrimPrefix(grandURI, "forkredis://"))
	require.NoError(t, err)
	defer grand.close()
	require.Equal(t, "upper", forkGet(t, grand, "Aa"))
	require.Empty(t, forkGet(t, grand, "Ab"))
}

func TestForkRedisTransactionSnapshot(t *testing.T) {
	c, _ := forkRedisFixture(t)
	forkPut(t, c, "Aa", "old")
	forkPut(t, c, "Ab", "old")
	require.NoError(t, c.txn(context.Background(), func(tx *kvTxn) error {
		require.Equal(t, "old", string(tx.get([]byte("Aa"))))
		forkPut(t, c, "Ab", "new")
		forkPut(t, c, "Ac", "created after snapshot")
		require.Equal(t, "old", string(tx.get([]byte("Ab"))), "transaction must not mix revisions")
		var names []string
		tx.scan([]byte("A"), []byte("B"), false, func(k, v []byte) bool {
			names = append(names, string(k)+"="+string(v))
			return true
		})
		require.Equal(t, []string{"Aa=old", "Ab=old"}, names)
		return nil
	}, 0))
	err := c.txn(context.Background(), func(tx *kvTxn) error {
		_ = tx.get([]byte("Aa"))
		forkPut(t, c, "Aa", "other writer")
		tx.set([]byte("Ab"), []byte("conflicting"))
		return nil
	}, 0)
	require.ErrorIs(t, err, redis.TxFailedErr)
	require.Equal(t, "new", forkGet(t, c, "Ab"))
}

func TestForkRedisGuardsAndPrivateRecords(t *testing.T) {
	c, uri := forkRedisFixture(t)
	require.ErrorIs(t, SealWorkspace(Background(), uri), ErrNotFormatted)
	forkPut(t, c, "setting", `{"Name":"test"}`)
	forkPut(t, c, "Flock", "runtime lock")
	forkPut(t, c, "Aempty", "")
	require.NoError(t, SealWorkspace(Background(), uri))
	childURI, err := ForkWorkspace(Background(), uri, "child")
	require.NoError(t, err)
	child, err := newForkRedisClient(childURI)
	require.NoError(t, err)
	defer child.close()
	require.NoError(t, child.simpleTxn(Background(), func(tx *kvTxn) error {
		require.Nil(t, tx.get([]byte("Flock")), "runtime locks must not be inherited")
		require.NotNil(t, tx.get([]byte("Aempty")), "empty values are not tombstones")
		return nil
	}, 0))
	_, err = newForkRedisClient(strings.Replace(childURI, "branch=child", "branch=missing", 1))
	require.ErrorIs(t, err, syscall.ENOENT)
	_, err = ForkWorkspace(Background(), uri, "../invalid")
	require.Error(t, err)
	require.NoError(t, c.rdb.HSet(Background(), c.head("child"), "schema", "999").Err())
	_, err = newForkRedisClient(childURI)
	require.ErrorContains(t, err, "unsupported")
}

func forkOpenMeta(t *testing.T, uri string, load bool) Meta {
	t.Helper()
	m, err := newForkMeta("forkredis", strings.TrimPrefix(uri, "forkredis://"), testConfig())
	require.NoError(t, err)
	if load {
		_, err = m.Load(true)
		require.NoError(t, err)
	}
	t.Cleanup(func() { require.NoError(t, m.Shutdown()) })
	return m
}

func TestForkRedisMetadataIsolation(t *testing.T) {
	_, uri := forkRedisFixture(t)
	source := forkOpenMeta(t, uri, false)
	format := testFormat()
	format.EnableACL = true
	require.NoError(t, source.Init(format, false))
	require.NoError(t, source.NewSession(true))
	ctx := Background()
	var dir, file, link Ino
	require.Zero(t, source.Mkdir(ctx, RootInode, "dir", 0755, 0, 0, &dir, nil))
	require.Zero(t, source.Mknod(ctx, dir, "file", TypeFile, 0644, 0, 0, "", &file, nil))
	require.Zero(t, source.Link(ctx, file, dir, "hard", nil))
	require.Zero(t, source.Symlink(ctx, dir, "sym", "file", &link, nil))
	require.Zero(t, source.SetXattr(ctx, file, "user.test", []byte("lower"), 0))
	var original uint64
	require.Zero(t, source.NewSlice(ctx, &original))
	require.Zero(t, source.Write(ctx, file, 0, 0, Slice{Id: original, Size: 1024, Len: 1024}, time.Now()))
	require.ErrorIs(t, SealWorkspace(ctx, uri), syscall.EBUSY)
	require.NoError(t, source.CloseSession())
	require.NoError(t, SealWorkspace(ctx, uri))
	childURI, err := ForkWorkspace(ctx, uri, "child")
	require.NoError(t, err)
	siblingURI, err := ForkWorkspace(ctx, uri, "sibling")
	require.NoError(t, err)
	child := forkOpenMeta(t, childURI, true)
	sibling := forkOpenMeta(t, siblingURI, true)
	for _, m := range []Meta{child, sibling} {
		require.NoError(t, m.NewSession(true))
		t.Cleanup(func() { require.NoError(t, m.CloseSession()) })
	}
	// The existing recursive clone still works inside a writable workspace.
	var copied, total uint64
	require.Zero(t, child.Clone(ctx, RootInode, dir, RootInode, "copy", 0, 0, CLONE_DEFAULT_CONCURRENCY, &copied, &total))
	require.Equal(t, uint64(4), copied) // directory, file, hardlink name, symlink
	var copyDir, copyFile Ino
	var copyAttr Attr
	require.Zero(t, child.Lookup(ctx, RootInode, "copy", &copyDir, &copyAttr, false))
	require.Zero(t, child.Lookup(ctx, copyDir, "file", &copyFile, &copyAttr, false))
	require.NotEqual(t, file, copyFile)
	var childID, siblingID uint64
	require.Zero(t, child.NewSlice(ctx, &childID))
	require.Zero(t, sibling.NewSlice(ctx, &siblingID))
	require.NotEqual(t, original, childID)
	require.NotEqual(t, childID, siblingID)
	require.Zero(t, child.Write(ctx, file, 0, 0, Slice{Id: childID, Size: 4, Len: 4}, time.Now()))
	require.Zero(t, child.Truncate(ctx, file, 0, 8, nil, false))
	require.Zero(t, child.SetXattr(ctx, file, "user.test", []byte("upper"), 0))
	require.Zero(t, child.Unlink(ctx, dir, "file"))
	require.Zero(t, child.Unlink(ctx, dir, "sym"))
	require.Zero(t, child.Rename(ctx, RootInode, "dir", RootInode, "moved", 0, nil, nil))
	var copySlices []Slice
	require.Zero(t, child.Read(ctx, copyFile, 0, &copySlices))
	require.Equal(t, original, copySlices[0].Id)
	require.Zero(t, child.GetAttr(ctx, copyFile, &copyAttr))
	require.Equal(t, uint64(1024), copyAttr.Length)
	var value []byte
	require.Zero(t, child.GetXattr(ctx, file, "user.test", &value))
	require.Equal(t, "upper", string(value))
	for _, m := range []Meta{source, sibling} {
		require.Zero(t, m.GetXattr(ctx, file, "user.test", &value))
		require.Equal(t, "lower", string(value))
		require.Zero(t, m.ReadLink(ctx, link, &value))
		require.Equal(t, "file", string(value))
		var a Attr
		var ino Ino
		require.Zero(t, m.Lookup(ctx, dir, "file", &ino, &a, false))
		require.Equal(t, file, ino)
		require.Equal(t, uint64(1024), a.Length)
		require.Equal(t, uint32(2), a.Nlink)
		require.Zero(t, m.Lookup(ctx, RootInode, "dir", &ino, &a, false))
		require.Equal(t, syscall.ENOENT, m.Lookup(ctx, RootInode, "moved", &ino, &a, false))
	}
	var entries []*Entry
	require.Zero(t, child.Readdir(ctx, dir, 1, &entries))
	var names []string
	for _, e := range entries {
		if string(e.Name) != "." && string(e.Name) != ".." {
			names = append(names, string(e.Name))
		}
	}
	require.Equal(t, []string{"hard"}, names)
	reopened := forkOpenMeta(t, childURI, true)
	var slices []Slice
	require.Zero(t, reopened.Read(ctx, file, 0, &slices))
	require.NotEmpty(t, slices)
	require.Equal(t, childID, slices[0].Id)
	var a Attr
	require.Zero(t, reopened.GetAttr(ctx, file, &a))
	require.Equal(t, uint64(8), a.Length)
	require.Equal(t, syscall.EROFS, source.SetXattr(ctx, file, "user.test", []byte("stale"), 0))
	readonly := forkOpenMeta(t, uri, true)
	require.True(t, readonly.getBase().conf.ReadOnly)
	require.Error(t, child.Reset())
	require.Error(t, child.DumpMeta(nil, RootInode, 1, false, false, false))
	deleted := false
	child.OnMsg(DeleteSlice, func(...interface{}) error { deleted = true; return nil })
	child.getBase().deleteSlice_(original, 1024)
	require.False(t, deleted, "shared object deletion callback must never run")
}

func TestForkRedisSealFencesInflightWriter(t *testing.T) {
	c, uri := forkRedisFixture(t)
	forkPut(t, c, "setting", `{"Name":"test"}`)
	err := c.txn(context.Background(), func(tx *kvTxn) error {
		tx.set([]byte("Aa"), []byte("not committed"))
		require.NoError(t, SealWorkspace(context.Background(), uri))
		return nil
	}, 0)
	require.ErrorIs(t, err, syscall.EROFS)
	require.Empty(t, forkGet(t, c, "Aa"))
}

func TestForkRedisForkDoesNotCopyRecords(t *testing.T) {
	for _, count := range []int{10, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			c, uri := forkRedisFixture(t)
			require.NoError(t, c.txn(context.Background(), func(tx *kvTxn) error {
				tx.set([]byte("setting"), []byte(`{"Name":"test"}`))
				for i := 0; i < count; i++ {
					tx.set([]byte(fmt.Sprintf("A%06d", i)), []byte("lower"))
				}
				return nil
			}, 0))
			require.NoError(t, SealWorkspace(context.Background(), uri))
			before, err := c.rdb.Keys(context.Background(), c.prefix+"*").Result()
			require.NoError(t, err)
			for i := 0; i < 3; i++ {
				_, err := ForkWorkspace(context.Background(), uri, fmt.Sprintf("c%d", i))
				require.NoError(t, err)
				n, err := c.rdb.ZCard(context.Background(), c.index(fmt.Sprintf("c%d", i))).Result()
				require.NoError(t, err)
				require.Zero(t, n)
			}
			after, err := c.rdb.Keys(context.Background(), c.prefix+"*").Result()
			require.NoError(t, err)
			require.Len(t, after, len(before)+3, "only three child headers may be added")
		})
	}
}

// Reuse upstream POSIX tests on a writable child of a sealed volume. These
// helpers neither reset the volume nor change its immutable format.
func TestForkRedisSharedOperations(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, Meta)
	}{
		{"xattr", testXattr},
		{"chown", testChownPermission},
		{"sticky", testStickyBit},
		{"locks", testLocks},
		{"resolve", testResolve},
		{"copy_file_range", testCopyFileRange},
		{"concurrent_write", testConcurrentWrite},
		{"batch_clone", testBatchClone},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, uri := forkRedisFixture(t)
			source := forkOpenMeta(t, uri, false)
			require.NoError(t, source.Init(testFormat(), false))
			require.NoError(t, SealWorkspace(Background(), uri))
			childURI, err := ForkWorkspace(Background(), uri, "child")
			require.NoError(t, err)
			child := forkOpenMeta(t, childURI, true)
			if test.name != "concurrent_write" && test.name != "batch_clone" {
				require.NoError(t, child.NewSession(true))
				t.Cleanup(func() { require.NoError(t, child.CloseSession()) })
			}
			test.run(t, child)
		})
	}
}

func TestForkRedisPrunesOnlyUnusedVersions(t *testing.T) {
	c, _ := forkRedisFixture(t)
	forkPut(t, c, "Aa", "snapshot")
	require.NoError(t, c.simpleTxn(context.Background(), func(tx *kvTxn) error {
		for i := 0; i < 20; i++ {
			forkPut(t, c, "Aa", fmt.Sprint(i))
		}
		require.Equal(t, "snapshot", string(tx.get([]byte("Aa"))))
		return nil
	}, 0))
	forkPut(t, c, "Aa", "latest")
	n, err := c.rdb.ZCard(context.Background(), c.record(c.branch, []byte("Aa"))).Result()
	require.NoError(t, err)
	require.LessOrEqual(t, n, int64(2), "finished readers must not pin history")
	require.Equal(t, "latest", forkGet(t, c, "Aa"))
}

func TestForkRedisExpiredSnapshotCannotSucceed(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		t.Run(fmt.Sprint(readOnly), func(t *testing.T) {
			c, _ := forkRedisFixture(t)
			forkPut(t, c, "Aa", "old")
			err := c.transaction(context.Background(), func(tx *kvTxn) error {
				tokens, err := c.rdb.ZRange(context.Background(), c.readers(), 0, -1).Result()
				require.NoError(t, err)
				require.Len(t, tokens, 1)
				require.NoError(t, c.rdb.ZAdd(context.Background(), c.readerExpiry(), redis.Z{Score: 0, Member: tokens[0]}).Err())
				forkPut(t, c, "Aa", "new") // reclaims the expired reader
				if !readOnly {
					tx.set([]byte("Ab"), []byte("must not commit"))
				}
				return nil
			}, 0, readOnly)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Empty(t, forkGet(t, c, "Ab"))
		})
	}
}

func TestForkRedisReadCacheConsistency(t *testing.T) {
	c, uri := forkRedisFixture(t)
	for key, value := range map[string]string{"setting": `{"Name":"cache"}`, "Aa": "lower", "Ab": "delete", "Aempty": ""} {
		forkPut(t, c, key, value)
	}
	require.NoError(t, SealWorkspace(Background(), uri))
	childURI, err := ForkWorkspace(Background(), uri, "child")
	require.NoError(t, err)
	child, err := newForkRedisClient(childURI)
	require.NoError(t, err)
	defer child.close()
	writer, err := newForkRedisClient(childURI)
	require.NoError(t, err)
	defer writer.close()
	read := func(client *forkRedisClient) map[string]string {
		got := make(map[string]string)
		require.NoError(t, client.scan([]byte("A"), func(k, v []byte) bool { got[string(k)] = string(v); return true }))
		return got
	}
	require.Equal(t, map[string]string{"Aa": "lower", "Ab": "delete", "Aempty": ""}, read(child))
	before := child.readBatches.Load()
	require.Equal(t, map[string]string{"Aa": "lower", "Ab": "delete", "Aempty": ""}, read(child))
	require.Equal(t, before, child.readBatches.Load(), "hot range and records need no Redis read pipeline")
	require.NoError(t, child.simpleTxn(Background(), func(tx *kvTxn) error {
		v := tx.get([]byte("Aa"))
		v[0] = 'X'
		require.Equal(t, "lower", string(tx.get([]byte("Aa"))), "returned bytes must not alias cached values")
		require.NotNil(t, tx.get([]byte("Aempty")))
		require.Nil(t, tx.get([]byte("Amissing")))
		var count int
		tx.scan([]byte("Z"), []byte("ZZ"), false, func(k, v []byte) bool { count++; return true })
		require.Zero(t, count) // cache the empty upper range too
		return nil
	}, 0))
	// A separate client changes keys and directories after this reader's snapshot.
	require.NoError(t, child.simpleTxn(Background(), func(tx *kvTxn) error {
		require.NoError(t, writer.txn(Background(), func(w *kvTxn) error {
			w.set([]byte("Aa"), []byte("upper"))
			w.delete([]byte("Ab"))
			w.set([]byte("Amissing"), []byte("created"))
			w.set([]byte("Za"), []byte("new entry"))
			return nil
		}, 0))
		require.Equal(t, "lower", string(tx.get([]byte("Aa"))))
		require.Equal(t, "delete", string(tx.get([]byte("Ab"))))
		require.Nil(t, tx.get([]byte("Amissing")))
		return nil
	}, 0))
	require.Equal(t, map[string]string{"Aa": "upper", "Aempty": "", "Amissing": "created"}, read(child))
	require.Equal(t, "new entry", forkGet(t, child, "Za"))
	var names []string
	require.NoError(t, child.scan([]byte("Z"), func(k, v []byte) bool { names = append(names, string(k)); return true }))
	require.Equal(t, []string{"Za"}, names)
	require.Equal(t, "lower", forkGet(t, c, "Aa"))
	// A warmed old child cannot write after another client seals it.
	require.NoError(t, SealWorkspace(Background(), childURI))
	require.ErrorIs(t, child.txn(Background(), func(tx *kvTxn) error { tx.set([]byte("Aa"), []byte("stale")); return nil }, 0), syscall.EROFS)
}

func TestForkRedisBatchAndCacheLimits(t *testing.T) {
	c, uri := forkRedisFixture(t)
	batch, err := newForkRedisClient(uri + "&fork_cache_bytes=0")
	require.NoError(t, err)
	defer batch.close()
	keys := make([][]byte, forkReadBatchKeys*2+1)
	require.NoError(t, c.txn(Background(), func(tx *kvTxn) error {
		for i := range keys {
			keys[i] = []byte(fmt.Sprintf("A%04d", i))
			tx.set(keys[i], keys[i])
		}
		return nil
	}, 0))
	before := batch.readBatches.Load()
	require.NoError(t, batch.simpleTxn(Background(), func(tx *kvTxn) error {
		require.Equal(t, keys, tx.gets(keys...))
		return nil
	}, 0))
	require.Equal(t, uint64(3), batch.readBatches.Load()-before)
	require.Empty(t, batch.cache.entries)
	require.NoError(t, c.txn(Background(), func(tx *kvTxn) error {
		tx.scan(keys[0], nextKey(keys[len(keys)-1]), false, func(k, v []byte) bool {
			if string(k) == string(keys[0]) {
				tx.set(keys[1], []byte("in-transaction"))
				tx.delete(keys[2])
			}
			if string(k) == string(keys[1]) {
				require.Equal(t, "in-transaction", string(v))
			}
			require.NotEqual(t, string(keys[2]), string(k), "batch scan must see callback deletes")
			return true
		})
		return nil
	}, 0))
	for _, option := range []string{"-1", "oops", "1073741825"} {
		_, _, _, _, err := parseForkURL(uri + "&fork_cache_bytes=" + option)
		require.Error(t, err)
	}
	cache := newForkReadCache(600)
	a, b, d := forkCacheKey{begin: "a"}, forkCacheKey{begin: "b"}, forkCacheKey{begin: "d"}
	cache.put(a, forkCacheValue{})
	cache.put(b, forkCacheValue{})
	_, ok := cache.get(a)
	require.True(t, ok)
	cache.put(d, forkCacheValue{})
	_, ok = cache.get(b)
	require.False(t, ok, "evict least recently used entry")
	cache.put(forkCacheKey{begin: "oversize"}, forkCacheValue{data: make([]byte, 601)})
	require.LessOrEqual(t, cache.used, cache.limit)
	require.Len(t, cache.entries, 2)
}

func TestForkRedisFailedReadsDoNotPublishCache(t *testing.T) {
	c, _ := forkRedisFixture(t)
	forkPut(t, c, "Aa", "old")
	var key forkCacheKey
	err := c.simpleTxn(Background(), func(tx *kvTxn) error {
		key = forkCacheKey{layer: c.branch, revision: tx.id(), begin: "Aa"}
		require.Equal(t, "old", string(tx.get([]byte("Aa"))))
		return errors.New("abort")
	}, 0)
	require.ErrorContains(t, err, "abort")
	_, ok := c.cache.get(key)
	require.False(t, ok)
	err = c.simpleTxn(Background(), func(tx *kvTxn) error {
		tokens, err := c.rdb.ZRange(Background(), c.readers(), 0, -1).Result()
		require.NoError(t, err)
		require.Len(t, tokens, 1)
		require.NoError(t, c.rdb.ZAdd(Background(), c.readerExpiry(), redis.Z{Score: 0, Member: tokens[0]}).Err())
		require.Equal(t, "old", string(tx.get([]byte("Aa"))))
		return nil
	}, 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, ok = c.cache.get(key)
	require.False(t, ok, "expired reads cannot populate the shared cache")
}

func TestForkRedisCachedReadValidation(t *testing.T) {
	c, uri := forkRedisFixture(t)
	forkPut(t, c, "Aa", "old-a")
	forkPut(t, c, "Ab", "old-b")
	require.NoError(t, c.simpleTxn(Background(), func(tx *kvTxn) error {
		tx.scan([]byte("A"), []byte("B"), false, func(k, v []byte) bool { return true })
		require.Nil(t, tx.get([]byte("missing")))
		return nil
	}, 0))
	begin, end := c.phases[forkPhaseBegin].calls.Load(), c.phases[forkPhaseEnd].calls.Load()
	checks := c.phases[forkPhaseCachedValidate].calls.Load()
	var got []string
	handled, err := c.cachedRead(Background(), func(tx *kvTxn) error {
		tx.scan([]byte("A"), []byte("B"), false, func(k, v []byte) bool { got = append(got, string(k)+"="+string(v)); return true })
		require.Nil(t, tx.get([]byte("missing")))
		return nil
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, []string{"Aa=old-a", "Ab=old-b"}, got)
	require.Equal(t, checks+1, c.phases[forkPhaseCachedValidate].calls.Load())
	require.Equal(t, begin, c.phases[forkPhaseBegin].calls.Load())
	require.Equal(t, end, c.phases[forkPhaseEnd].calls.Load())
	n, err := c.rdb.ZCard(Background(), c.readers()).Result()
	require.NoError(t, err)
	require.Zero(t, n, "complete local results require no Redis retention pin")
	// A new revision between collection and validation must discard the result.
	writer, err := newForkRedisClient(uri)
	require.NoError(t, err)
	defer writer.close()
	handled, err = c.cachedRead(Background(), func(tx *kvTxn) error {
		require.Equal(t, "old-a", string(tx.get([]byte("Aa"))))
		forkPut(t, writer, "Aa", "new-a")
		return nil
	})
	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, "new-a", forkGet(t, c, "Aa"))
	checks = c.phases[forkPhaseCachedValidate].calls.Load()
	handled, err = c.cachedRead(Background(), func(tx *kvTxn) error {
		_ = tx.get([]byte("Aa"))
		_ = tx.get([]byte("not-cached"))
		return nil
	})
	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, checks, c.phases[forkPhaseCachedValidate].calls.Load(), "cache miss must do no speculative Redis I/O")
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if expired {
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
		}
		handled, err = c.cachedRead(ctx, func(tx *kvTxn) error {
			_ = tx.get([]byte("Aa"))
			if expired {
				time.Sleep(3 * time.Millisecond)
			} else {
				cancel()
			}
			return nil
		})
		cancel()
		require.True(t, handled)
		if expired {
			require.ErrorIs(t, err, context.DeadlineExceeded)
		} else {
			require.ErrorIs(t, err, context.Canceled)
		}
	}
	handled, err = c.cachedRead(Background(), func(tx *kvTxn) error { tx.set([]byte("Aa"), []byte("not allowed")); return nil })
	require.True(t, handled)
	require.ErrorIs(t, err, syscall.EROFS)
	require.Equal(t, "new-a", forkGet(t, c, "Aa"))
}

func TestForkRedisCachedMetadataFallback(t *testing.T) {
	_, uri := forkRedisFixture(t)
	m := forkOpenMeta(t, uri, false)
	require.NoError(t, m.Init(testFormat(), false))
	fork := m.(*forkMeta)
	c := fork.client.(*forkRedisClient)
	ctx := Background()
	var inode Ino
	require.Zero(t, m.Mknod(ctx, RootInode, "old", TypeFile, 0644, 0, 0, "", &inode, nil))
	// Warm exact point/range paths using the normal API.
	var attr Attr
	var found Ino
	for i := 0; i < 2; i++ {
		require.Zero(t, m.Lookup(ctx, RootInode, "old", &found, &attr, false))
		require.Zero(t, m.GetAttr(ctx, inode, &attr))
	}
	writer := forkOpenMeta(t, uri, true)
	require.Zero(t, writer.Truncate(ctx, inode, 0, 123, nil, false))
	require.Zero(t, m.GetAttr(ctx, inode, &attr))
	require.Equal(t, uint64(123), attr.Length)
	require.Zero(t, writer.Rename(ctx, RootInode, "old", RootInode, "new", 0, nil, nil))
	require.Equal(t, syscall.ENOENT, m.Lookup(ctx, RootInode, "old", &found, &attr, false))
	require.Zero(t, m.Lookup(ctx, RootInode, "new", &found, &attr, false))
	require.Equal(t, inode, found)
	// Missing a later prefetched record cannot append speculative entries twice.
	require.Zero(t, writer.Mknod(ctx, RootInode, "second", TypeFile, 0644, 0, 0, "", nil, nil))
	begin, end := fork.entryKey(RootInode, ""), nextKey(fork.entryKey(RootInode, ""))
	keys, _, err := fork.scan(begin, end, -1, nil)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	head := c.observedHead.Load()
	ck := forkCacheKey{layer: c.branch, revision: head.revision, begin: string(keys[1])}
	c.cache.mu.Lock()
	if e := c.cache.entries[ck]; e != nil {
		c.cache.used -= e.Value.(forkCacheEntry).cost
		c.cache.lru.Remove(e)
		delete(c.cache.entries, ck)
	}
	c.cache.mu.Unlock()
	keys, _, err = fork.scan(begin, end, -1, nil)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.NotEqual(t, string(keys[0]), string(keys[1]))
	require.Zero(t, writer.Unlink(ctx, RootInode, "new"))
	require.Equal(t, syscall.ENOENT, m.Lookup(ctx, RootInode, "new", &found, &attr, false))
}

// Opt-in metadata-only measurement. Both paths use this experimental engine;
// it is not a benchmark of the upstream Redis driver's absolute performance.
func TestForkRedisScaling(t *testing.T) {
	output := os.Getenv("JFS_FORK_BENCH_OUTPUT")
	if output == "" {
		t.Skip("set JFS_FORK_BENCH_OUTPUT to record the opt-in scaling experiment")
	}
	_, err := os.Stat(output)
	require.True(t, os.IsNotExist(err), "do not overwrite previous measurements")
	executable, err := os.Executable()
	require.NoError(t, err)
	binary, err := os.ReadFile(executable)
	require.NoError(t, err)
	digest := sha256.Sum256(binary)
	report := map[string]interface{}{
		"test_binary_sha256": hex.EncodeToString(digest[:]), "go_version": runtime.Version(),
		"gomaxprocs": runtime.GOMAXPROCS(0), "passed": false,
		"scope": "metadata API only; zero-length files; both algorithms on forkredis; no FUSE/mount cost",
	}
	var rows []map[string]interface{}
	defer func() {
		report["rows"] = rows
		report["passed"] = !t.Failed()
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, append(data, '\n'), 0644))
	}()
	ctx := Background()
	for _, count := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			client, uri := forkRedisFixture(t)
			source := forkOpenMeta(t, uri, false)
			require.NoError(t, source.Init(testFormat(), false))
			require.NoError(t, source.NewSession(true))
			var seed Ino
			require.Zero(t, source.Mkdir(ctx, RootInode, "seed", 0755, 0, 0, &seed, nil))
			for i := 0; i < count; i++ {
				require.Zero(t, source.Mknod(ctx, seed, fmt.Sprintf("f%05d", i), TypeFile, 0644, 0, 0, "", nil, nil))
			}
			require.NoError(t, source.CloseSession())
			require.NoError(t, SealWorkspace(ctx, uri))
			for pair := 0; pair < 3; pair++ {
				order := []string{"lazy", "recursive"}
				if pair%2 == 1 {
					order = []string{"recursive", "lazy"}
				}
				for _, method := range order {
					name := fmt.Sprintf("%s%d", method, pair)
					var childURI string
					var child Meta
					if method == "recursive" {
						childURI, err = ForkWorkspace(ctx, uri, name)
						require.NoError(t, err)
						child = forkOpenMeta(t, childURI, true)
						require.NoError(t, child.NewSession(true))
					}
					before, err := client.rdb.Info(ctx, "commandstats").Result()
					require.NoError(t, err)
					start := time.Now()
					if method == "lazy" {
						childURI, err = ForkWorkspace(ctx, uri, name)
						require.NoError(t, err)
					} else {
						var copied, total uint64
						require.Zero(t, child.Clone(ctx, RootInode, seed, RootInode, "copy", 0, 0, CLONE_DEFAULT_CONCURRENCY, &copied, &total))
						require.Equal(t, uint64(count+1), copied)
					}
					elapsed := time.Since(start).Nanoseconds()
					after, err := client.rdb.Info(ctx, "commandstats").Result()
					require.NoError(t, err)
					records, err := client.rdb.ZCard(ctx, client.index(name)).Result()
					require.NoError(t, err)
					if method == "lazy" {
						require.Zero(t, records)
						child = forkOpenMeta(t, childURI, true)
					} else {
						require.GreaterOrEqual(t, records, int64(count*2))
					}
					var inode Ino
					var attr Attr
					dir := "seed"
					if method == "recursive" {
						dir = "copy"
					}
					require.Zero(t, child.Lookup(ctx, RootInode, dir, &inode, &attr, false))
					var entries []*Entry
					require.Zero(t, child.Readdir(ctx, inode, 0, &entries))
					files := 0
					for _, e := range entries {
						if strings.HasPrefix(string(e.Name), "f") {
							files++
						}
					}
					require.Equal(t, count, files)
					if method == "recursive" {
						require.NoError(t, child.CloseSession())
					}
					row := map[string]interface{}{
						"files": count, "pair": pair, "method": method, "wall_ns": elapsed,
						"upper_records": records, "verified_files": files,
						"redis_before": before, "redis_after": after,
					}
					rows = append(rows, row)
					t.Logf("files=%d pair=%d method=%s ns=%d upper_records=%d", count, pair, method, elapsed, records)
				}
			}
		})
	}
}
