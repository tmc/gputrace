# Buffer Storage Mode in Metal Capture Records

Status: settled 2026-08-29 by controlled experiment on Apple M4 Max
(macOS Darwin 25.6.0).

## Finding

[V] The 8 bytes at offset +0x18 of a `Culul` buffer-creation record hold the
`MTLResourceOptions` value passed at creation, verbatim.

Established with a controlled capture (`testdata/storage-mode-probe/main.m`)
that creates buffers with five distinct options values and distinct lengths,
plus one heap-allocated buffer, then reads +0x18 per record keyed by length:

| length | options at creation | Culul+0x18 |
|--------|--------------------|------------|
| 4096   | 0x0 shared default | 0x0 |
| 8192   | 0x20 private | 0x20 |
| 12288  | 0x100 shared+untracked | 0x100 |
| 16384  | 0x120 private+untracked | 0x120 |
| 20480  | 0x200 shared+tracked | 0x200 |
| 24576  | heap buffer, 0x20 private | 0x20 |

Every value round-trips exactly, including hazard-tracking and storage-mode
bits independently, so the field is the full options word — not a
heap-vs-direct flag (both populations appear on both sides) and not a
hazard-only flag.

Prior state: the field was read by nothing, and
`internal/trace/api_calls.go` printed a hardcoded
`options:HazardTrackingModeUntracked` for every buffer — a label, not a
decode. An earlier survey correctly reported the constant 256 (0x100) across
all 2,177 buffers of a qwen-decode capture; that is simply how MLX
allocates: shared storage, untracked hazard mode.

## What consumes it

- `internal/trace/api_calls.go` decodes the field into `InitCall.ResourceOptions`
  and renders it with `FormatResourceOptions` (storage mode, CPU cache mode,
  hazard tracking mode components).
- `Trace.BufferStorageModes()` counts buffer-creation records by storage
  mode; an empty map means the bundle has no buffer-creation records, which
  is distinct from zero buffers of some mode.
- `gputrace gate` prints the breakdown as a staging observation, e.g.
  `storage: buffer storage modes: 2177 shared (capture buffer-creation records)`
  or `storage: no buffer-creation records in bundle` (profiler-only traces).

## What remains open

- [?] `CU` heap-creation records reference an `MTLHeapDescriptor` payload by
  a 16-hex content key that matches no file in the bundle; the heap's own
  storageMode is not yet decodable (heap buffers still carry their per-buffer
  options word, which covers the practical cases).
- [?] streamData and MTSP device-resources records carry no storage-mode
  data at all (exhaustive string and marker scan, 2026-08-29); the Culul
  record in the capture stream is currently the only source.
