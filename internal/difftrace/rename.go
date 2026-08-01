package difftrace

import "fmt"

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
// Pairing is by count. The names of a renamed kernel need not resemble each
// other -- gg2_copybfloat16bfloat16 and gg2_dynamic_copybfloat16bfloat16 do,
// but a JIT fusion renamed from Broadcast to Multiply does not -- and an exact
// count match on opposite sides is the stronger signal in any case. Name
// similarity is used only to break a tie between candidates that already share
// the count, never as a condition on a count match that is otherwise unique.

// nameMatchMargin is how much better the best candidate's similarity must be
// than the runner up's before a tie is broken. A rename that has to be picked
// out of several equal-count candidates is only worth asserting when one
// candidate is clearly closer than the rest; a near tie is reported as
// ambiguous instead.
const nameMatchMargin = 0.15

// A tiebreak also needs the names to actually share structure: half the longer
// name, in one run of at least minSharedRun characters. Two mangled kernel
// names that describe the same work share a long run -- gg2_copybfloat16bfloat16
// sits whole inside gg2_dynamic_copybfloat16bfloat16 -- while a stray matching
// character or two is a coincidence, not a rename.
const (
	minNameSimilarity = 0.5
	minSharedRun      = 4
)

// RenamePairs maps each side of a likely rename to its counterpart. A count
// shared by exactly one A-only and one B-only function pairs them on the count
// alone. When several functions on a side share the count, the pair is taken
// only if one candidate on each side is the other's most similar name by a
// clear margin; otherwise the group is left unpaired, because guessing would
// state a relationship the trace does not record.
func RenamePairs(deltas []FunctionDelta) map[string]string {
	pairs, _ := renamePairing(deltas)
	return pairs
}

// AmbiguousRenames reports, per one-sided function, how many equal-count
// candidates on the other side it could not be told apart from. Callers render
// these as unpaired rather than pairing them arbitrarily.
func AmbiguousRenames(deltas []FunctionDelta) map[string]int {
	_, ambiguous := renamePairing(deltas)
	return ambiguous
}

// AmbiguousNote describes an unpaired one-sided row: how many equal-count
// candidates stood opposite it, and that none of them was decisive. It says
// what is unresolved rather than asserting a pairing the trace does not
// record.
func AmbiguousNote(candidates int) string {
	if candidates == 1 {
		return "1 rename candidate with the same count, not decisive"
	}
	return fmt.Sprintf("%d rename candidates with the same count, none decisive", candidates)
}

func renamePairing(deltas []FunctionDelta) (map[string]string, map[string]int) {
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
	ambiguous := map[string]int{}
	for count, a := range onlyA {
		b := onlyB[count]
		if len(b) == 0 {
			continue
		}
		if len(a) == 1 && len(b) == 1 {
			pairs[a[0]] = b[0]
			pairs[b[0]] = a[0]
			continue
		}
		matched := breakTie(a, b)
		for name, partner := range matched {
			pairs[name] = partner
		}
		// The candidates a row was not told apart from are the ones still
		// unpaired; a candidate that settled elsewhere is no longer in doubt.
		leftA, leftB := remaining(a, matched), remaining(b, matched)
		for _, name := range leftA {
			if len(leftB) > 0 {
				ambiguous[name] = len(leftB)
			}
		}
		for _, name := range leftB {
			if len(leftA) > 0 {
				ambiguous[name] = len(leftA)
			}
		}
	}
	return pairs, ambiguous
}

// breakTie pairs equal-count candidates whose names single each other out. A
// pair is taken only when each name is the other's most similar and that
// similarity beats every rival on both sides by nameMatchMargin. Settled pairs
// are then removed and the rest reconsidered: once a group has come down to
// one candidate on each side, the equal and opposite counts pair them by
// themselves, names notwithstanding. What survives that is left for the caller
// to report as ambiguous.
func breakTie(as, bs []string) map[string]string {
	matched := map[string]string{}
	for {
		as, bs = remaining(as, matched), remaining(bs, matched)
		if len(as) == 0 || len(bs) == 0 {
			return matched
		}
		if len(as) == 1 && len(bs) == 1 {
			matched[as[0]] = bs[0]
			matched[bs[0]] = as[0]
			return matched
		}
		settled := false
		for _, a := range as {
			b, ok := bestMatch(a, bs)
			if !ok {
				continue
			}
			back, ok := bestMatch(b, as)
			if !ok || back != a {
				continue
			}
			matched[a], matched[b] = b, a
			settled = true
		}
		if !settled {
			return matched
		}
	}
}

func remaining(names []string, matched map[string]string) []string {
	left := names[:0:0]
	for _, n := range names {
		if _, ok := matched[n]; !ok {
			left = append(left, n)
		}
	}
	return left
}

// bestMatch returns the most similar candidate, if one leads the rest by
// nameMatchMargin. A candidate that merely edges out the others is no
// evidence of a rename.
func bestMatch(name string, candidates []string) (string, bool) {
	best, second := -1.0, -1.0
	var bestName string
	for _, c := range candidates {
		s := nameSimilarity(name, c)
		switch {
		case s > best:
			best, second, bestName = s, best, c
		case s > second:
			second = s
		}
	}
	if bestName == "" || best-second < nameMatchMargin || best < minNameSimilarity ||
		longestCommonSubstring(name, bestName) < minSharedRun {
		return "", false
	}
	return bestName, true
}

// nameSimilarity scores two kernel names by the length of their longest
// common run of characters, relative to the longer name. Mangled Metal
// function names carry their shared structure as a contiguous run --
// gg2_copybfloat16bfloat16 inside gg2_dynamic_copybfloat16bfloat16 -- which a
// shared prefix alone would miss.
func nameSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	longest := longestCommonSubstring(a, b)
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	return float64(longest) / float64(n)
}

func longestCommonSubstring(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
	}
	return best
}
