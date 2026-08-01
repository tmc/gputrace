package timing

// A timing row is a measurement: a span, a call count, and a share of the
// total. When a trace carries no profiler payload and no capture-derived
// timing, none of those three things exist, and the earlier fallback invented
// all of them -- a per-name duration guessed from the kernel's name, one call
// apiece, and shares computed off the guesses.
//
// A header saying "synthetic (approximate)" does not undo that. The table
// below it is still sorted by cost, still carries per-kernel percentages, and
// still reads as a ranking of where the time went, because that is what a
// sorted table with a Share column is. The numbers were not merely uncertain,
// they were fabricated, and one reader ranked a kernel at 3.3% of a span the
// trace never measured.
//
// So refuse. Print what the trace does structurally say -- how many command
// buffers and encoders it holds -- and say plainly that it cannot say how long
// anything took. This mirrors what the kernel inventory already does when its
// dispatch join yields nothing: print the absence, not a zero.

// TimingSourceUnavailable marks a trace that carries no timing measurement at
// all. It is distinct from an approximate source: approximate means measured
// badly, unavailable means not measured.
const TimingSourceUnavailable TimingSource = "unavailable"

const unavailableTimingNote = "This trace carries no profiler payload and no capture-derived encoder\n" +
	"timing, so per-function spans, call counts and shares are unavailable.\n" +
	"The dispatches happened; this trace cannot say how long they took.\n" +
	"\n" +
	"Capture with --profile, or open a .gpuprofiler_raw export, to get timing.\n"

// UnavailableTimingNote explains an empty timing table. It is what the table
// is replaced by, not a caption printed above one.
func UnavailableTimingNote() string {
	return unavailableTimingNote
}
