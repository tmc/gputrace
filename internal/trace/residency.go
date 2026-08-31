package trace

import "sort"

// A StorageFootprint is what one Metal storage mode costs in a capture.
type StorageFootprint struct {
	Mode    string `json:"mode"`
	Buffers int    `json:"buffers"`
	Bytes   uint64 `json:"bytes"`
}

// ResidencyCalls counts the three calls that make residency explicit.
//
// A capture that creates a residency set and never requests it, or never adds
// it to a queue, is not managing residency: the set exists and does nothing,
// and every allocation falls to the driver's automatic residency instead.
type ResidencyCalls struct {
	NewResidencySet  int `json:"new_residency_set"`
	RequestResidency int `json:"request_residency"`
	AddResidencySet  int `json:"add_residency_set"`
}

// Explicit reports whether the capture actually commits a residency set.
func (c ResidencyCalls) Explicit() bool {
	return c.NewResidencySet > 0 && c.RequestResidency > 0 && c.AddResidencySet > 0
}

// Any reports whether the capture holds any residency record at all. It is the
// difference between observing that a program does not manage residency and
// decoding nothing about residency either way.
func (c ResidencyCalls) Any() bool {
	return c.NewResidencySet > 0 || c.RequestResidency > 0 || c.AddResidencySet > 0
}

// A ResidencyReport says how a capture allocates memory and whether it manages
// residency itself.
//
// The two halves belong together. An all-shared allocation profile and an
// uncommitted residency set are the same finding seen from two directions: the
// process is leaving placement and residency to the driver. Reading either one
// alone invites the wrong conclusion, which is why they are reported as one
// thing rather than as a storage-mode count in the gate and a list of API calls
// in "api-calls".
type ResidencyReport struct {
	// Storage is the per-mode footprint, largest first.
	Storage []StorageFootprint `json:"storage"`
	// Buffers and Bytes are the totals across every mode.
	Buffers int    `json:"buffers"`
	Bytes   uint64 `json:"bytes"`

	Residency ResidencyCalls `json:"residency"`

	// Unsized counts buffer-creation records whose length field was zero. A
	// zero-length buffer is not a thing Metal creates, so these are records the
	// scan matched and did not fully decode, and Bytes understates by however
	// much they held.
	Unsized int `json:"unsized"`
}

// ResidencyReport builds the report for a trace.
//
// It reads the decoded init calls rather than rescanning the capture, so its
// numbers agree with "gputrace api-calls" by construction. That matters more
// than the cost of the parse: a second scanner that disagrees with the first is
// worse than no second scanner, because both look authoritative.
//
// The init records this reads are found by scanning the whole capture for
// record markers, which is independent of the dispatch decoding that
// "api-calls" reports coverage for. A capture whose dispatches are almost
// entirely undecoded can still have a complete buffer and residency picture,
// and saying otherwise would understate a number that is not in doubt. The
// real limit is different and narrower: the scan finds the record shapes it
// knows, so a shape it does not know is missing rather than counted.
func (t *Trace) ResidencyReport() (*ResidencyReport, error) {
	calls, err := t.InitCalls()
	if err != nil {
		return nil, err
	}
	byMode := map[string]*StorageFootprint{}
	r := &ResidencyReport{}
	for _, c := range calls {
		switch c.Type {
		case "newBuffer":
			mode := StorageModeName(c.ResourceOptions)
			f := byMode[mode]
			if f == nil {
				f = &StorageFootprint{Mode: mode}
				byMode[mode] = f
			}
			f.Buffers++
			f.Bytes += c.Length
			r.Buffers++
			r.Bytes += c.Length
			if c.Length == 0 {
				r.Unsized++
			}
		case "newResidencySet":
			r.Residency.NewResidencySet++
		case "requestResidency":
			r.Residency.RequestResidency++
		case "addResidencySet":
			r.Residency.AddResidencySet++
		}
	}
	for _, f := range byMode {
		r.Storage = append(r.Storage, *f)
	}
	sort.Slice(r.Storage, func(i, j int) bool {
		if r.Storage[i].Bytes != r.Storage[j].Bytes {
			return r.Storage[i].Bytes > r.Storage[j].Bytes
		}
		return r.Storage[i].Mode < r.Storage[j].Mode
	})
	return r, nil
}

// Finding states what the report shows, or says that it shows nothing.
//
// The wording is deliberately about what was and was not observed. A capture
// with no buffer-creation records is not a capture of a program that allocates
// nothing, and a reader who is handed "0 private buffers" will believe the
// second thing.
func (r *ResidencyReport) Finding() string {
	if r.Buffers == 0 && !r.Residency.Any() {
		return "This capture yielded no buffer-creation and no residency records, " +
			"so it says nothing about either. That is a property of the capture, " +
			"not a description of the program."
	}
	if r.Buffers == 0 {
		return "No buffer-creation records in this capture, so it says nothing about storage modes."
	}
	var shared uint64
	for _, f := range r.Storage {
		if f.Mode == "shared" {
			shared = f.Bytes
		}
	}
	allShared := len(r.Storage) == 1 && r.Storage[0].Mode == "shared"

	switch {
	case allShared && !r.Residency.Explicit():
		return "Every buffer is shared storage and no residency set is committed, " +
			"so placement and residency are both left to the driver."
	case allShared:
		return "Every buffer is shared storage, though residency is managed explicitly."
	case r.Bytes > 0 && shared*2 > r.Bytes && !r.Residency.Explicit():
		return "Most bytes are in shared storage and no residency set is committed."
	case !r.Residency.Explicit() && r.Residency.NewResidencySet > 0:
		return "A residency set was created and never committed: it has no effect, " +
			"and every allocation falls to the driver's automatic residency."
	}
	return ""
}
