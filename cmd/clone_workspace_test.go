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

package cmd

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestCloneWorkspaceArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--lazy", "--seal", "forkredis://127.0.0.1/0?volume=v"},
		{"--lazy", "-p", "forkredis://127.0.0.1/0?volume=v", "child"},
		{"--lazy", "--threads=4", "forkredis://127.0.0.1/0?volume=v", "child"},
		{"--lazy", "one"},
		{"--seal", "one", "two"},
	} {
		app := &cli.App{Commands: []*cli.Command{cmdClone()}, Writer: io.Discard, ErrWriter: io.Discard}
		require.Error(t, app.Run(append([]string{"juicefs", "clone"}, args...)))
	}
}
