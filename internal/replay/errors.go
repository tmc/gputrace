package replay

import (
	"errors"
	"fmt"
)

// ErrICBExecutionUnsupported is returned when a replay plan contains an
// indirect command buffer execution. The current replay state does not
// reconstruct MTLIndirectCommandBuffer objects from traces.
var ErrICBExecutionUnsupported = errors.New("indirect command buffer execution cannot be replayed")

func unsupportedICBExecutionError(cmd ReplayCommand) error {
	return fmt.Errorf("%w: sequence=%d encoder=%d icb=0x%x count=%d",
		ErrICBExecutionUnsupported,
		cmd.SequenceNum,
		cmd.EncoderIndex,
		cmd.ICBAddr,
		cmd.ICBCount,
	)
}
