// Package parity compares gputrace's per-encoder counter values against the
// values Xcode reports for the same capture.
//
// The oracle is a set of tab-separated exports of Xcode's Counters sub-tabs,
// one file per tab, all keyed by the same encoder Name column. See
// testdata/xcode-oracle/PROVENANCE.md for how they were produced and for the
// cells that are known to be wrong in Xcode's own output.
//
// The point of the package is to answer, per column, whether gputrace produces
// the value at all. A column we do not produce is reported as NotProduced. It
// is never reported as a match, and never defaulted to "0.00".
package parity

import (
	"bufio"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Oracle is the joined Xcode counter table: one row per encoder, one column per
// metric, with the metric columns of every sub-tab merged into a single space.
type Oracle struct {
	// Encoders holds the join key of each encoder in capture order: the
	// cumulative end offset in microseconds. Xcode's Counters.csv names it
	// "Encoder FunctionIndex"; the sub-tab exports bury it as the leading
	// number of the encoder's display name.
	Encoders []string
	// Display holds Xcode's full name for each encoder, parallel to Encoders.
	Display []string
	Columns []Column            // metric columns, sorted by name
	values  map[string][]string // column name -> value per encoder
	// Skipped names the exports in the directory that are not encoder-keyed
	// and so contribute no column, such as Xcode's pipeline-keyed Shaders
	// tab. It is reported rather than dropped: a tab silently missing from
	// the union would understate the oracle without changing any count.
	Skipped []string
}

// DisplayName returns Xcode's full name for an encoder join key.
func (o *Oracle) DisplayName(key string) string {
	for i, k := range o.Encoders {
		if k == key {
			if o.Display[i] != "" {
				return o.Display[i]
			}
			return key
		}
	}
	return key
}

// Column describes one metric column of the oracle.
type Column struct {
	Name string // metric name as Xcode spells it
	Tab  string // source file base name, without extension
	// Populated reports whether any encoder has a value that is neither empty
	// nor zero. An unpopulated column carries no information about this capture.
	Populated bool
	// Constant reports whether every encoder has the same value.
	Constant bool
	// DuplicateOf names another column with byte-identical values in every row,
	// or is empty. Xcode prints some counters twice under different labels.
	DuplicateOf string
	// ReformattedTabs lists sub-tabs that print the same numbers with a
	// different unit suffix than the tab this column was first read from.
	ReformattedTabs []string
	// Sources names the exports this column was found in.
	Sources []string
	// RepeatedHeaders is how many times the column name appears in a single
	// export's header row, when more than once.
	RepeatedHeaders int
}

// Value returns the oracle value for a column and encoder.
func (o *Oracle) Value(column, encoder string) (string, bool) {
	col, ok := o.values[column]
	if !ok {
		return "", false
	}
	for i, e := range o.Encoders {
		if e == encoder {
			return col[i], true
		}
	}
	return "", false
}

// Column returns the named column.
func (o *Oracle) Column(name string) (Column, bool) {
	for _, c := range o.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// CountersCSVName is the Counters.csv export inside the oracle directory.
const CountersCSVName = "xcode-counters-export.csv"

// Load reads every Xcode export in dir and merges them into one oracle.
//
// Two independent exports of the same capture cover overlapping but different
// counter sets, so neither alone is the universe: Counters.csv carries 29
// fragment-shader columns the sub-tabs omit, and the sub-tabs carry Execution
// Cost and seven bandwidth columns Counters.csv omits. Load returns the union,
// plus every cell on which the two exports disagree by more than rounding.
func Load(fsys fs.FS, dir string) (*Oracle, []Disagreement, error) {
	tabs, err := LoadOracle(fsys, dir)
	if err != nil {
		return nil, nil, err
	}
	csvPath := path.Join(dir, CountersCSVName)
	if _, err := fs.Stat(fsys, csvPath); err != nil {
		return tabs, nil, nil
	}
	joined, err := LoadCountersCSV(fsys, csvPath)
	if err != nil {
		return nil, nil, err
	}
	return Merge(tabs, joined)
}

// LoadOracle reads every encoder-keyed .txt export in dir and joins them on
// encoder name.
//
// Every encoder-keyed file must list the same encoders in the same order; a
// file that does not is a sign that the exports came from different captures,
// and is an error rather than something to reconcile. Files that re-export a
// tab already seen are checked for agreement and then dropped, which is what
// makes them evidence that Xcode's export is deterministic.
//
// A tab whose rows carry no encoder join key at all is a different row space
// rather than a disagreement, and is skipped and named in Oracle.Skipped. A tab
// where only some rows carry one is neither, and is an error.
func LoadOracle(fsys fs.FS, dir string) (*Oracle, error) {
	names, err := fs.Glob(fsys, path.Join(dir, "*.txt"))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("parity: no oracle exports in %s", dir)
	}
	sort.Strings(names)

	o := &Oracle{values: make(map[string][]string)}
	seenTab := make(map[string]string)       // column -> tab that first defined it
	reformatted := make(map[string][]string) // column -> tabs that render it differently

	for _, name := range names {
		tab := strings.TrimSuffix(path.Base(name), ".txt")
		header, rows, err := readTSV(fsys, name)
		if err != nil {
			return nil, err
		}
		encoders, err := encoderColumn(header, rows)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		keys := make([]string, len(encoders))
		withKey := 0
		for i, e := range encoders {
			keys[i] = JoinKey(e)
			if keys[i] != "" {
				withKey++
			}
		}
		// Not every .txt Xcode writes into a capture directory is an encoder
		// tab. The Shaders tab is keyed by kernel function and pipeline state,
		// so its rows carry no leading cumulative-offset number and belong to a
		// different row space entirely. Comparing its names to the encoder list
		// reports "not from one capture", which sends the reader hunting for a
		// capture mixup that did not happen.
		switch {
		case withKey == 0:
			o.Skipped = append(o.Skipped, path.Base(name))
			continue
		case withKey != len(keys):
			return nil, fmt.Errorf("%s: %d of %d rows carry an encoder join key; the tab mixes row spaces",
				name, withKey, len(keys))
		}
		if o.Encoders == nil {
			o.Encoders, o.Display = keys, encoders
		} else if !equalStrings(o.Encoders, keys) {
			return nil, fmt.Errorf("%s: encoder list differs from earlier encoder-keyed exports; the files are not from one capture", name)
		}

		for ci, colName := range header {
			colName = strings.TrimSpace(colName)
			if colName == "" || colName == "Name" || colName == "Thumbnails" {
				continue
			}
			vals := make([]string, len(rows))
			for ri, row := range rows {
				if ci < len(row) {
					vals[ri] = strings.TrimSpace(row[ci])
				}
			}
			if prev, ok := o.values[colName]; ok {
				if equalStrings(prev, vals) {
					continue
				}
				if !numericallyEqual(prev, vals) {
					return nil, fmt.Errorf("%s: column %q disagrees numerically with the export in %s; Xcode's export is supposed to be deterministic",
						name, colName, seenTab[colName])
				}
				// Same numbers, different rendering. Xcode prints some counters
				// with their unit suffix in one tab and bare in another --
				// "Register L1 Read Accesses" is "2.27%" on the Memory tab and
				// "2.27" on Performance Limiters. Record it; a harness that
				// compared these as strings would call the tabs inconsistent.
				reformatted[colName] = append(reformatted[colName], tab)
				continue
			}
			o.values[colName] = vals
			seenTab[colName] = tab
		}
	}

	if o.Encoders == nil {
		return nil, fmt.Errorf("parity: no encoder-keyed export in %s; skipped %s",
			dir, strings.Join(o.Skipped, ", "))
	}

	for name, vals := range o.values {
		o.Columns = append(o.Columns, Column{
			Name:            name,
			Tab:             seenTab[name],
			Populated:       populated(vals),
			Constant:        constant(vals),
			ReformattedTabs: reformatted[name],
			Sources:         []string{"sub-tab exports"},
		})
	}
	sort.Slice(o.Columns, func(i, j int) bool { return o.Columns[i].Name < o.Columns[j].Name })
	o.markDuplicates()
	return o, nil
}

// markDuplicates records, for each column, an earlier column with identical
// values in every row. Two counters that never differ carry one counter's worth
// of information between them.
func (o *Oracle) markDuplicates() {
	for i := range o.Columns {
		if !o.Columns[i].Populated {
			continue
		}
		for j := 0; j < i; j++ {
			if !o.Columns[j].Populated {
				continue
			}
			if equalStrings(o.values[o.Columns[i].Name], o.values[o.Columns[j].Name]) {
				o.Columns[i].DuplicateOf = o.Columns[j].Name
				break
			}
		}
	}
}

func readTSV(fsys fs.FS, name string) (header []string, rows [][]string, err error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if header == nil {
			header = fields
			continue
		}
		rows = append(rows, fields)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	if header == nil {
		return nil, nil, fmt.Errorf("%s: empty export", name)
	}
	return header, rows, nil
}

func encoderColumn(header []string, rows [][]string) ([]string, error) {
	idx := -1
	for i, h := range header {
		if strings.TrimSpace(h) == "Name" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("no Name column")
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		if idx >= len(row) {
			return nil, fmt.Errorf("row %d has no Name field", i+1)
		}
		names[i] = strings.TrimSpace(row[idx])
		if names[i] == "" {
			return nil, fmt.Errorf("row %d has an empty Name", i+1)
		}
	}
	return names, nil
}

// populated reports whether any row of a column holds a non-zero value.
func populated(vals []string) bool {
	for _, v := range vals {
		if v == "" {
			continue
		}
		f, err := ParseNumber(v)
		if err != nil {
			return true // non-numeric text is information
		}
		if f != 0 {
			return true
		}
	}
	return false
}

func constant(vals []string) bool {
	for i := 1; i < len(vals); i++ {
		if vals[i] != vals[0] {
			return false
		}
	}
	return true
}

// byteUnits scale a magnitude to bytes. The sub-tab exports render byte
// counters as "2.21 MiB" while the Counters.csv export renders the same
// counter as "2312832.00", so the two only reconcile once the unit is applied.
var byteUnits = map[string]float64{
	"byte": 1, "bytes": 1,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
	"KB": 1e3, "MB": 1e6, "GB": 1e9,
}

// ParseNumber parses a value as Xcode formats it. Thousands separators and a
// trailing percent sign are stripped; a trailing byte unit is applied as a
// scale factor; any other trailing unit (GiB/s, Calls) is dropped, since both
// exports use the same one for a given counter.
func ParseNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSpace(s)

	unit := ""
	if i := strings.IndexAny(s, " "); i > 0 {
		unit, s = strings.TrimSpace(s[i+1:]), s[:i]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if scale, ok := byteUnits[unit]; ok {
		v *= scale
	}
	return v, nil
}

// numericallyEqual reports whether two renderings of a column hold the same
// numbers, ignoring unit suffixes and thousands separators.
func numericallyEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		x, err1 := ParseNumber(a[i])
		y, err2 := ParseNumber(b[i])
		if err1 != nil || err2 != nil || x != y {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
