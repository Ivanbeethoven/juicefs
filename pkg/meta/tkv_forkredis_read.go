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
	"errors"
	"strconv"
	"time"
)

var errForkCacheMiss = errors.New("fork read snapshot is not fully cached")

func (c *forkRedisClient) observeHead(head forkLayer) {
	for {
		old := c.observedHead.Load()
		if old != nil && old.revision >= head.revision {
			return
		}
		if c.observedHead.CompareAndSwap(old, &head) {
			return
		}
	}
}

// cachedRead is only for side-effect-free collectors. It does not invoke the
// callback again or expose any result itself. On false, the caller discards all
// speculative output and uses its normal pinned transaction. In particular,
// generic simpleTxn/txn callbacks are never speculatively replayed.
func (c *forkRedisClient) cachedRead(ctx context.Context, collect func(*kvTxn) error) (handled bool, err error) {
	if c.cache.limit == 0 || c.observedHead.Load() == nil {
		return false, nil
	}
	defer c.phases[forkPhaseCachedTotal].observe(time.Now())
	ctx, cancel := context.WithTimeout(ctx, forkTransactionTTL)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return true, err
	}
	head := *c.observedHead.Load()
	tx := &forkRedisTxn{c: c, ctx: ctx, layers: append([]forkLayer{head}, c.parents...), readOnly: true, cacheOnly: true}
	missing := false
	collectErr := func() (err error) {
		defer c.phases[forkPhaseCachedPrepare].observe(time.Now())
		defer func() {
			if p := recover(); p != nil {
				if e, ok := p.(error); ok {
					if e == errForkCacheMiss {
						missing = true
					} else {
						err = e
					}
				} else {
					panic(p)
				}
			}
		}()
		return collect(&kvTxn{kvtxn: tx})
	}()
	if missing {
		c.cachedReadFallbacks.Add(1)
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	// Every value/range was copied from a previously validated, immutable cache
	// entry at this revision. Checking the head now linearizes the entire result.
	// No old Redis versions will be fetched after this point, so no retention pin
	// or separate release is needed. A later writer cannot alter our local bytes.
	started := time.Now()
	values, err := c.rdb.HMGet(ctx, c.head(c.branch), "revision", "sealed").Result()
	c.phases[forkPhaseCachedValidate].observe(started)
	if err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	sealed := "0"
	if head.sealed {
		sealed = "1"
	}
	if len(values) != 2 || values[0] != strconv.FormatUint(head.revision, 10) || values[1] != sealed {
		c.cachedReadFallbacks.Add(1)
		return false, nil
	}
	c.cachedReadHits.Add(1)
	return true, collectErr
}
