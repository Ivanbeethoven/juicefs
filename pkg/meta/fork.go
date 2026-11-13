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
	"fmt"
	"io"
)

func init() { Register("forkredis", newForkMeta) }

// forkMeta keeps the existing KV/POSIX implementation and exposes only the
// administrative operations that understand shared layers. This format is
// separate from the standard Redis engine and is experimental.
type forkMeta struct{ *kvMeta }

var _ Meta = (*forkMeta)(nil)

func newForkMeta(_ string, addr string, conf *Config) (Meta, error) {
	client, err := newForkRedisClient(addr)
	if err != nil {
		return nil, err
	}
	head, err := client.readHead(context.Background(), client.branch)
	if err != nil {
		_ = client.close()
		return nil, err
	}
	// Retain object data until shared-root-aware GC exists. These effective
	// settings are also visible to the mount's caller/configuration output.
	conf.MaxDeletes = 0
	conf.NoBGJob = true
	conf.NoCompact = true
	conf.ReadOnly = conf.ReadOnly || head.sealed
	m := &kvMeta{baseMeta: newBaseMeta(addr, conf), client: client}
	m.en = m
	return &forkMeta{m}, nil
}

func (m *forkMeta) Init(format *Format, force bool) error {
	if format.ChangeLog {
		return fmt.Errorf("forkredis does not support changelog replay")
	}
	previous, err := m.en.doLoad()
	if err != nil {
		return err
	}
	if previous != nil {
		return fmt.Errorf("forkredis format/config changes are unsupported after initialization")
	}
	return m.baseMeta.Init(format, force)
}

func (m *forkMeta) Load(checkVersion bool) (*Format, error) {
	f, err := m.baseMeta.Load(checkVersion)
	if err == nil && f.ChangeLog {
		return nil, fmt.Errorf("forkredis does not support changelog replay")
	}
	return f, err
}

func (m *forkMeta) OnMsg(kind uint32, callback MsgCallback) {
	if kind == DeleteSlice {
		m.baseMeta.OnMsg(kind, func(...interface{}) error {
			return fmt.Errorf("forkredis retains shared object data; collection is not implemented")
		})
		return
	}
	m.baseMeta.OnMsg(kind, callback)
}

func forkUnsupported(operation string) error {
	return fmt.Errorf("%s is unsupported for forkredis: it must preserve shared layers and object references", operation)
}
func (m *forkMeta) DumpMeta(io.Writer, Ino, int, bool, bool, bool) error {
	return forkUnsupported("dump")
}
func (m *forkMeta) LoadMeta(io.Reader) error                         { return forkUnsupported("load") }
func (m *forkMeta) DumpMetaV2(Context, io.Writer, *DumpOption) error { return forkUnsupported("dump") }
func (m *forkMeta) LoadMetaV2(Context, io.Reader, *LoadOption) error { return forkUnsupported("load") }
func (m *forkMeta) ScanDeletedObject(Context, trashSliceScan, pendingSliceScan, trashFileScan, pendingFileScan) error {
	return forkUnsupported("gc")
}
