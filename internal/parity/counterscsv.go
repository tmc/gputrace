package parity

import (
	"encoding/csv"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// CountersCSVMetadataColumns are the leading non-counter columns of Xcode's
// Counters.csv export. Column 5 is blank.
//
// "Encoder FunctionIndex" is the join key: Xcode names outright the number that
// the sub-tab exports bury in the encoder's display name, and it is the
// encoder's cumulative end offset in microseconds.
var CountersCSVMetadataColumns = []string{
	"Index", "Encoder FunctionIndex", "CommandBuffer Label", "Encoder Label", "",
}

// LoadCountersCSV reads Xcode's Counters.csv export, which is the whole
// Counters tab already joined: one row per encoder, one column per counter.
//
// Sixteen column names appear twice in the export. Every duplicated pair is
// byte-identical in all rows, so it is an export quirk rather than two distinct
// counters sharing a name, but a reader that keys on the header name silently
// keeps whichever occurrence it saw last. This function checks the pairs and
// records them on the column; it never drops one silently.
func LoadCountersCSV(fsys fs.FS, name string) (*Oracle, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("%s: no data rows", name)
	}
	header, rows := records[0], records[1:]

	keyCol := -1
	for i, h := range header {
		if strings.TrimSpace(h) == "Encoder FunctionIndex" {
			keyCol = i
			break
		}
	}
	if keyCol < 0 {
		return nil, fmt.Errorf("%s: no Encoder FunctionIndex column", name)
	}

	labelCol := -1
	for i, h := range header {
		if strings.TrimSpace(h) == "Encoder Label" {
			labelCol = i
			break
		}
	}

	o := &Oracle{values: make(map[string][]string)}
	for _, row := range rows {
		if keyCol >= len(row) {
			return nil, fmt.Errorf("%s: row is missing the join key", name)
		}
		key := strings.TrimSpace(row[keyCol])
		o.Encoders = append(o.Encoders, key)
		display := key
		if labelCol >= 0 && labelCol < len(row) {
			display = key + " " + strings.TrimSpace(row[labelCol])
		}
		o.Display = append(o.Display, display)
	}

	dupes := make(map[string][]int)
	for ci, colName := range header {
		colName = strings.TrimSpace(colName)
		if colName == "" || isMetadataColumn(colName) {
			continue
		}
		dupes[colName] = append(dupes[colName], ci)
	}

	var conflicting []string
	for colName, cols := range dupes {
		vals := columnValues(rows, cols[0])
		for _, ci := range cols[1:] {
			other := columnValues(rows, ci)
			if !equalStrings(vals, other) {
				conflicting = append(conflicting, colName)
			}
		}
		c := Column{
			Name:      colName,
			Tab:       "Counters.csv",
			Populated: populated(vals),
			Constant:  constant(vals),
			Sources:   []string{"Counters.csv"},
		}
		if len(cols) > 1 {
			c.RepeatedHeaders = len(cols)
		}
		o.values[colName] = vals
		o.Columns = append(o.Columns, c)
	}
	if len(conflicting) > 0 {
		sort.Strings(conflicting)
		return nil, fmt.Errorf("%s: repeated header(s) %v hold different values; they are distinct counters and cannot be keyed by name",
			name, conflicting)
	}

	sort.Slice(o.Columns, func(i, j int) bool { return o.Columns[i].Name < o.Columns[j].Name })
	o.markDuplicates()
	return o, nil
}

func isMetadataColumn(name string) bool {
	for _, m := range CountersCSVMetadataColumns {
		if m != "" && m == name {
			return true
		}
	}
	return false
}

func columnValues(rows [][]string, ci int) []string {
	vals := make([]string, len(rows))
	for i, row := range rows {
		if ci < len(row) {
			vals[i] = strings.TrimSpace(row[ci])
		}
	}
	return vals
}
