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
	"fmt"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cloneWorkspace(ctx *cli.Context) error {
	if ctx.Bool("lazy") && ctx.Bool("seal") {
		return fmt.Errorf("--lazy and --seal are separate operations")
	}
	if ctx.Bool("preserve") || ctx.IsSet("threads") {
		return fmt.Errorf("lazy workspaces preserve all metadata; --preserve and --threads do not apply")
	}
	expected := 2
	if ctx.Bool("seal") {
		expected = 1
	}
	if ctx.NArg() != expected {
		return fmt.Errorf("use clone --seal META-URL, or clone --lazy META-URL CHILD")
	}
	setup(ctx, expected)
	uri := ctx.Args().Get(0)
	if ctx.Bool("seal") {
		if err := meta.SealWorkspace(ctx.Context, uri); err != nil {
			return err
		}
		_, err := fmt.Fprintln(ctx.App.Writer, "Sealed read-only workspace:", utils.RemovePassword(uri))
		return err
	}
	child, err := meta.ForkWorkspace(ctx.Context, uri, ctx.Args().Get(1))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ctx.App.Writer, utils.RemovePassword(child))
	return err
}
