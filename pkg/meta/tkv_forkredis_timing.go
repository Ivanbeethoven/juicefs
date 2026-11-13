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
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	forkPhaseTotal = iota
	forkPhaseBegin
	forkPhaseBody
	forkPhaseDataIO
	forkPhaseEnd
	forkPhaseCommit
	forkPhaseCleanup
	forkPhasePublish
	forkPhaseCount
)

var forkPhaseNames = [...]string{"transaction", "begin", "body", "data_io", "end", "commit", "cleanup", "publish"}

type forkPhaseMetric struct {
	calls atomic.Uint64
	nanos atomic.Uint64
}

func (p *forkPhaseMetric) observe(start time.Time) {
	p.nanos.Add(uint64(time.Since(start)))
	p.calls.Add(1)
}

func (c *forkRedisClient) initTimingMetrics(reg prometheus.Registerer) {
	for i, name := range forkPhaseNames {
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "forkredis_phase_seconds_total", Help: "Cumulative client elapsed time; body includes data_io, transaction includes all pinned phases.",
			ConstLabels: prometheus.Labels{"phase": name},
		}, func() float64 { return float64(c.phases[i].nanos.Load()) / 1e9 }))
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "forkredis_phase_calls_total", Help: "Number of measured phase executions (not wire retries).",
			ConstLabels: prometheus.Labels{"phase": name},
		}, func() float64 { return float64(c.phases[i].calls.Load()) }))
	}
}
