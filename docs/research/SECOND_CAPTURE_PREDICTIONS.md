# Pre-registered tests for the second parity capture

Written before the second `.gpuprofiler_raw` or its Xcode export was available.
All comparisons below must use that one capture and its capture-matched Xcode
oracle. No identifier or number from `parity-asymmetric-perfdata.gputrace` may
be joined into it.

## Required input anchors

Before testing any candidate:

1. Verify the raw capture UUID equals the profiled bundle UUID and the Xcode
   export UUID.
2. Derive encoder sequence IDs, pipeline addresses, encoder ordinals, dispatch
   counts, and command-buffer timestamps again from the new capture.
3. Decode every `Counters_f_*.raw` and `Profiling_f_*.raw` with a fresh parser
   per file. Record the exact GPU generation/variant/revision, uarch behaviour,
   pulse/era/count periods, and parse flags supplied to the decoder.
4. Reject the capture for owner testing if no GPRWCNTR record has a non-machine-
   wide `EncoderID`, or if the intended long dispatches still fall below the
   sampler period.
5. Record complete-population cardinalities before intersections. A negative
   from one shard or one encoder is not a result.

## Refutation 1: APS exposes no encoder/pipeline owner

Current claim: [V] Xcode 26.4's complete exported APS profile-data accessor
surface has no encoder or pipeline accessor.

What a new capture cannot overturn: a capture cannot add an exported function
to the same framework binary. Re-run `nm -gU` only if the Xcode/framework build
changed.

What would overturn the broader conclusion that decoded APS data cannot supply
an owner:

- a safely copied, documented-shape APS field whose complete population maps
  functionally to independently derived capture-local encoder identity; and
- the mapping must cover every owner represented by the new asymmetric
  workload, with no one-to-many candidate key; and
- permuting encoder order in the workload must leave the content join intact.

Numerical proximity, matching row counts, or a match that exists only after
using ordinal position does not overturn it.

## Refutation 2: the trace builder receives the association from its caller

Current claim: [V] `agxps_trace_mtl_command_encoder_add_kick` accepts caller-
supplied `(encoderIndex, kickIndex)`, bounds-checks both, and stores the pair.

What a new capture cannot overturn: that ABI and behavior are properties of
the framework binary, not capture contents.

What would overturn the inference that the archived data lacks the same
association elsewhere:

- an archive record, decoded without raw-object reinterpretation, must contain
  both a kick key and a content-bearing encoder key; and
- across the complete fresh capture, kick-to-encoder must be functional and
  agree with an independently parsed encoder owner; and
- the same relationship must reproduce after encoder order is changed.

Finding that Xcode can construct the trace is insufficient: Xcode already has
the pre-replay encoder structure and may be supplying the association from
outside the archived APS data.

## Refutation 3: `kick_id` is parser-local, not a foreign key

Current bounded result: [V] fresh parsers on
`parity-asymmetric-perfdata.gputrace` produced dense local `kick_id` values
0..125 with repeats across 927 kick records; they had zero intersection with
49 GPRWCNTR `KickTraceID` values.

Prediction if the field is parser-local on the new capture:

- each independently created parser starts `kick_id` at zero or another fixed
  local base;
- separate shards reuse the same small dense IDs; and
- the full `kick_id` population has no substantial exact intersection with the
  fresh capture's GPRWCNTR `KickTraceID` population.

Results that would overturn this refutation:

- IDs remain stable for the same kick when the same file is decoded by two
  independent parsers and when the kick is represented in another shard; and
- `kick_id` exactly intersects a substantial fraction of capture-local GPR
  `KickTraceID`; and
- every intersecting `kick_id` maps to exactly one non-machine-wide
  `EncoderID` across the complete population.

All three conditions are required. Uniqueness by itself is not a join.

## Clock-domain test

Current bounded result: [V] the first capture's 40 fresh-decoded counter shards
share one system-timestamp window, ending 656.9 ms before its command-buffer
window under identity, and no validated transform connects them.

Pre-registered alternatives for the fresh capture:

1. **Identity domain:** decoded APS system timestamps overlap the same-capture
   GPRWCNTR or command-buffer interval without an offset.
2. **Published archive transform:** applying the capture's own
   `continuousTime-absoluteTime` offset produces overlap and preserves duration
   within measured sampling quantization.
3. **Paired-event affine transform:** at least two independently identified
   events present in both streams determine scale and offset; the transform is
   then scored on held-out paired events, not the anchors used to fit it.

The clock is proven only if one alternative predicts held-out events across
the full capture. Similar magnitudes, a plausible 24 MHz slope, or comparable
window durations are insufficient.

## Production gate

Even if an owner or clock test succeeds, no column is produced until the same
row is owner-joined without ordinal position, unit-resolved, and scored against
the new capture's Xcode export. A failure at any gate leaves the value absent.
