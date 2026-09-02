// Command testcensus renders `go test -json` output and reports how many
// tests were skipped.
//
// It exists because `ok` and a skip are indistinguishable in go test's default
// output. Much of this suite is opt-in integration coverage gated on an
// environment variable, so a run can report ok for every package while a large
// fraction of it never executed. That reads as "the suite passes" and means
// "the suite passes, minus whatever did not run", which is the same
// absent-rendered-as-a-value problem the tool spends its time finding in
// captures.
//
// Output is the per-package result lines plus a tally. Failures are printed in
// full; a nonzero exit is preserved.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type event struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

func main() {
	var pass, skip, fail int
	skippedBy := map[string]int{}
	failed := []string{}
	failOutput := map[string][]string{}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for in.Scan() {
		var e event
		if err := json.Unmarshal(in.Bytes(), &e); err != nil {
			// Not JSON: a build error or a panic go test did not wrap.
			// Pass it through rather than swallow it.
			fmt.Println(in.Text())
			continue
		}
		if e.Test == "" {
			if e.Action == "output" && (strings.HasPrefix(e.Output, "FAIL") || strings.HasPrefix(e.Output, "ok  ")) {
				fmt.Print(e.Output)
			}
			continue
		}
		key := e.Package + "." + e.Test
		switch e.Action {
		case "pass":
			pass++
		case "skip":
			skip++
			skippedBy[e.Package]++
		case "fail":
			fail++
			failed = append(failed, key)
		case "output":
			failOutput[key] = append(failOutput[key], e.Output)
		}
	}

	for _, k := range failed {
		fmt.Printf("\n--- FAIL: %s\n", k)
		for _, line := range failOutput[k] {
			fmt.Print(line)
		}
	}

	fmt.Printf("\n%d passed, %d skipped, %d failed\n", pass, skip, fail)
	if skip > 0 {
		// Named by package so the skips are attributable, not just counted.
		type pkgCount struct {
			pkg string
			n   int
		}
		var top []pkgCount
		for p, n := range skippedBy {
			top = append(top, pkgCount{p, n})
		}
		sort.Slice(top, func(i, j int) bool {
			if top[i].n != top[j].n {
				return top[i].n > top[j].n
			}
			return top[i].pkg < top[j].pkg
		})
		fmt.Println("skipped tests are opt-in integration coverage; `make test-gated` shows which")
		fmt.Println("variables are unset. Most skips concentrate in:")
		for i, t := range top {
			if i == 5 {
				break
			}
			fmt.Printf("  %3d  %s\n", t.n, t.pkg)
		}
	}
	if fail > 0 {
		os.Exit(1)
	}
}
