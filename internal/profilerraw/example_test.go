package profilerraw_test

import (
	"fmt"

	"github.com/tmc/gputrace/internal/profilerraw"
)

func ExampleRecords() {
	data := []byte{0x4e, 0, 0, 0, 1, 2, 0x4e, 0, 0, 0, 3}
	records := profilerraw.Records(data)
	fmt.Println(len(records), len(records[0].Data), len(records[1].Data))
	// Output: 2 6 5
}
