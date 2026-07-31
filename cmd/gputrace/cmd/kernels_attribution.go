package cmd

import "strconv"

// A kernel row's dispatch count is a join: the inventory supplies the function
// names, the command stream supplies the dispatches, and a Ctt record keyed by
// function address is what connects them. When a capture carries no resolvable
// Ctt mapping the join yields nothing, and every named kernel is left at zero
// beside a trace that plainly ran hundreds of dispatches.
//
// Zero is then the wrong thing to print. It is a number in a count column, so
// it reads as "this kernel never ran" -- a measurement -- when it means "this
// trace cannot say". Print the absence instead, and say so once above the
// table.

// unattributedDispatchMark stands in for a dispatch count that the trace
// cannot supply.
const unattributedDispatchMark = "—"

const unattributedInventoryNote = "No dispatch could be joined to a named function, so per-kernel counts are\n" +
	"unavailable for this trace and are shown as —. The dispatches happened; this\n" +
	"trace cannot say which kernel ran them.\n"

// unattributedInventory reports whether the trace ran dispatches but resolved
// none of them to a name, which makes every per-kernel count meaningless
// rather than zero.
func unattributedInventory(attributed, total int) bool {
	return total > 0 && attributed == 0
}

// formatDispatchCount renders a row's dispatch count, substituting the
// unattributed mark when no count in the table can be trusted as a count.
func formatDispatchCount(count, attributed, total int) string {
	if count == 0 && unattributedInventory(attributed, total) {
		return unattributedDispatchMark
	}
	return strconv.Itoa(count)
}
