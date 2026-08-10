// Copyright © 2026 gputrace authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.

package exp_test

import (
	"fmt"

	"github.com/tmc/gputrace/exp"
)

func ExampleCommand() {
	cmd := exp.Command("/tmp/libgputrace_capture.dylib", "python3", "-c", "import mlx.core as mx; mx.eval(mx.ones((10, 10)))")
	fmt.Println(cmd.Path != "")
	// Output:
	// true
}

func ExampleCommandWithOptions() {
	opts := exp.Options{
		OutputFile: "custom_events.json",
		FrameCount: 1,
	}
	cmd := exp.CommandWithOptions("/tmp/libgputrace_capture.dylib", opts, "echo", "hello")
	fmt.Println(len(cmd.Env) > 0)
	// Output:
	// true
}
