package counter

import (
	"encoding/binary"
	"fmt"
)

// GPRWCNTR record layout.
//
// A GPRWCNTR blob is a sequence of self-delimiting records:
//
//	record := "GPRWCNTR" (8 bytes) || ncols * uint64 (little-endian)
//	stride := 8 + 8*ncols
//
// The magic is per-record, not a one-time blob header, so the stride can be
// recovered from the blob itself: it is the distance to the second magic.
// ncols comes from the column-name list of the pass that produced the blob,
// and it is not a constant across containers. Measured in one archive:
// ShaderProfilerData blobs are uniformly 20 columns (stride 168) for RDE_0,
// 17 (144) for BMPR_RDE_0 and 9 (80) for Firmware, while the per-pass blobs
// under "Derived Counter Sample Data" range over 7..43 columns (64..352).
//
// The first seven columns are the same in every pass column list seen so far,
// in this order. [V] measured across every blob in two independent archives.
const (
	grcTimestamp = iota
	grcGPUCycles
	grcSampleType
	grcEncoderID
	grcKickTraceID
	grcKickSlotIdx
	grcSourceID
	grcNumFixedColumns
)

// GRCColumnNames are the fixed leading column names of a GPRWCNTR record, in
// record order. Columns beyond these are the pass's hardware counters.
var GRCColumnNames = [grcNumFixedColumns]string{
	"GRC_TIMESTAMP",
	"GRC_GPU_CYCLES",
	"GRC_SAMPLE_TYPE",
	"GRC_ENCODER_ID",
	"GRC_KICK_TRACE_ID",
	"GRC_KICK_SLOT_IDX",
	"GRC_SOURCE_ID",
}

// GRCMachineWideID is the GRC_ENCODER_ID and GRC_KICK_TRACE_ID value on
// samples that belong to no encoder in the capture. The GPU counter stream is
// machine-wide: it samples every process on the device, and work that is not
// the replay's own carries this id.
const GRCMachineWideID = 0xFFFFFFFF

// GRC_SAMPLE_TYPE values seen on samples that name an encoder of the capture.
// Each pass reads the counters once when the encoder starts and once when it
// ends, so an encoder appears twice per pass. The end record carries the
// counter deltas accumulated over the encoder, including GRC_GPU_CYCLES.
// [D] derived: in the reference archive every attributed sample is one of
// these two types (3,024 begins and 3,392 ends of 6,416), begins pair with
// ends by encoder id within a blob, and the begin records' counter columns are
// uniformly zero.
const (
	GRCSampleTypeEncoderBegin = 4
	GRCSampleTypeEncoderEnd   = 5
)

// GRCMachineWideSampleType is the GRC_SAMPLE_TYPE that accompanies
// GRCMachineWideID. [D] derived: it holds for all 552,308 RDE_0 records in the
// reference archive and all 807,444 in a second, independent one.
const GRCMachineWideSampleType = 6

// GPRWCNTRSample is one decoded GPRWCNTR record.
type GPRWCNTRSample struct {
	Timestamp   uint64   `json:"timestamp"`          // GRC_TIMESTAMP, mach-absolute ticks
	GPUCycles   uint64   `json:"gpu_cycles"`         // GRC_GPU_CYCLES
	SampleType  uint64   `json:"sample_type"`        // GRC_SAMPLE_TYPE
	EncoderID   uint64   `json:"encoder_id"`         // GRC_ENCODER_ID, joins to Encoder Infos
	KickTraceID uint64   `json:"kick_trace_id"`      // GRC_KICK_TRACE_ID
	KickSlotIdx uint64   `json:"kick_slot_idx"`      // GRC_KICK_SLOT_IDX
	SourceID    uint64   `json:"source_id"`          // GRC_SOURCE_ID
	Counters    []uint64 `json:"counters,omitempty"` // Remaining columns, in pass order
}

// MachineWide reports whether the sample belongs to no encoder in the capture.
// Such samples must not be folded into per-encoder figures: they are other
// processes' GPU work.
func (s GPRWCNTRSample) MachineWide() bool {
	return s.EncoderID == GRCMachineWideID
}

// GPRWCNTRStride returns the record stride of a GPRWCNTR blob, derived from the
// distance between the first two record magics. A blob holding a single record
// has no second magic, so its stride is its length.
//
// It returns an error unless the stride divides the blob exactly, which is the
// check that a wrong stride fails. Reporting the error matters more than the
// stride: the previous fixed-size parse produced plausible garbage silently.
func GPRWCNTRStride(data []byte) (int, error) {
	if len(data) < len(GPRWCNTRMagic) || string(data[:len(GPRWCNTRMagic)]) != GPRWCNTRMagic {
		return 0, fmt.Errorf("gprwcntr: missing magic")
	}
	stride := len(data)
	for i := len(GPRWCNTRMagic); i+len(GPRWCNTRMagic) <= len(data); i++ {
		if string(data[i:i+len(GPRWCNTRMagic)]) == GPRWCNTRMagic {
			stride = i
			break
		}
	}
	if stride < len(GPRWCNTRMagic)+grcNumFixedColumns*8 {
		return 0, fmt.Errorf("gprwcntr: stride %d is shorter than the fixed columns", stride)
	}
	if (stride-len(GPRWCNTRMagic))%8 != 0 {
		return 0, fmt.Errorf("gprwcntr: stride %d leaves a partial column", stride)
	}
	if len(data)%stride != 0 {
		return 0, fmt.Errorf("gprwcntr: stride %d does not divide blob length %d", stride, len(data))
	}
	return stride, nil
}

// ParseGPRWCNTR decodes every record in a GPRWCNTR blob. It also returns the
// stride it used, so callers can report the column count they actually saw.
func ParseGPRWCNTR(data []byte) ([]GPRWCNTRSample, int, error) {
	stride, err := GPRWCNTRStride(data)
	if err != nil {
		return nil, 0, err
	}
	ncols := (stride - len(GPRWCNTRMagic)) / 8

	samples := make([]GPRWCNTRSample, 0, len(data)/stride)
	for off := 0; off < len(data); off += stride {
		rec := data[off : off+stride]
		if string(rec[:len(GPRWCNTRMagic)]) != GPRWCNTRMagic {
			return nil, stride, fmt.Errorf("gprwcntr: record %d lacks magic", off/stride)
		}
		cols := rec[len(GPRWCNTRMagic):]
		col := func(i int) uint64 { return binary.LittleEndian.Uint64(cols[i*8:]) }

		s := GPRWCNTRSample{
			Timestamp:   col(grcTimestamp),
			GPUCycles:   col(grcGPUCycles),
			SampleType:  col(grcSampleType),
			EncoderID:   col(grcEncoderID),
			KickTraceID: col(grcKickTraceID),
			KickSlotIdx: col(grcKickSlotIdx),
			SourceID:    col(grcSourceID),
		}
		if ncols > grcNumFixedColumns {
			s.Counters = make([]uint64, ncols-grcNumFixedColumns)
			for i := range s.Counters {
				s.Counters[i] = col(grcNumFixedColumns + i)
			}
		}
		samples = append(samples, s)
	}
	return samples, stride, nil
}
