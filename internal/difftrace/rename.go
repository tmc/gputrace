package difftrace

// A kernel that is renamed between two traces appears twice in the delta
// table: once as an A-only row and once as a B-only row, with equal and
// opposite counts. Both rows are correct and neither is a change in the work
// done. Summed as if they were independent, they double a delta -- a hand
// rolled comparison of two such pairs reported +176 against a true +92.
//
// The table already sorts by the size of the delta, so the two halves of a
// pair land next to each other. What is missing is saying that they are two
// halves of one thing rather than two facts.
//
// Pairing is by count alone. The names of a renamed kernel need not resemble
// each other -- gg2_copybfloat16bfloat16 and gg2_dynamic_copybfloat16bfloat16
// do, but a JIT fusion renamed from Broadcast to Multiply does not -- and an
// exact count match on opposite sides is the stronger signal in any case.

// RenamePairs maps each side of a likely rename to its counterpart. A pair is
// reported only when exactly one A-only function and exactly one B-only
// function share a dispatch count: with two candidates on either side there is
// no evidence for which pairs with which, and guessing would state a
// relationship the trace does not record.
func RenamePairs(deltas []FunctionDelta) map[string]string {
	onlyA := map[int][]string{}
	onlyB := map[int][]string{}
	for _, d := range deltas {
		switch {
		case d.DispatchCountB == 0 && d.DispatchCountA > 0:
			onlyA[d.DispatchCountA] = append(onlyA[d.DispatchCountA], d.FunctionName)
		case d.DispatchCountA == 0 && d.DispatchCountB > 0:
			onlyB[d.DispatchCountB] = append(onlyB[d.DispatchCountB], d.FunctionName)
		}
	}

	pairs := map[string]string{}
	for count, a := range onlyA {
		b := onlyB[count]
		if len(a) != 1 || len(b) != 1 {
			continue
		}
		pairs[a[0]] = b[0]
		pairs[b[0]] = a[0]
	}
	return pairs
}
