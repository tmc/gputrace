# MTSP record framing, the xdic index, and texture data

How a `.gputrace` bundle is laid out, read by length prefix rather than by scanning for
markers. Checked against a 5,910-call render capture (a Direct3D 11 game through DXMT,
macOS 27, 2.6 GB) and every fixture in `testdata/traces`: all MTSP files walk to their
last byte, and every `index` parses to its last byte. Implemented in
`internal/trace/framed.go`, `storeindex.go` and `texturedata.go`; used by the `records`,
`blob` and `textures` commands.

## MTSP files

`capture`, `unsorted-capture`, `device-resources-*`, `delta-device-resources-*`,
`unused-device-resources-*`, `startup-*-platform`.

The file header is 8 bytes, `"MTSP"` and a u32 version (`0x400`); the first record starts
at offset 8. `ParseMTSPFromData` assumes 16 bytes and starts inside the first record,
which is why it falls back to marker scanning.

Each record is one API call:

| Offset | Size | Field |
|---|---|---|
| 0x00 | u32 | record size, including this header |
| 0x04 | i32 | function id |
| 0x08 | 24 | zero in every record seen |
| 0x20 | u32 | flags |
| 0x24 | | signature, NUL-terminated, padded to 4 |
| | | arguments, then optional blocks |

Function ids are stable across captures (`-10236` is a texture snapshot in every trace
checked). `-12272` is the exception: a generic state record whose signature varies.

### Signature tokens

| Token | Size | Meaning |
|---|---|---|
| `C` | 8 | receiver object |
| `t` | 8 | object argument |
| `ul` `l` `uw` `w` | 8 | 64-bit integer |
| `i` `ui` `f` | 4 | 32-bit integer, float |
| `us` `s` | 2 | |
| `uc` `c` `b` | 1 | |
| `S` | | string, NUL-terminated, padded to 4 |
| `U` | | name of out-of-line data: a 16-hex-digit index key or a bundle file name |
| `<b>` | 0 | the bytes named by the preceding `U` |
| `@N<tok>` | | array of N |

4- and 8-byte values are 4-aligned. With these sizes, 63,377 of the render capture's
63,398 records decode. The other 21 are `Culul@2ul@2ul@4ub` (token `ub` unknown) and
`CiuiSSS` (a string runs past the record).

### After the arguments

- Flag `0x1`: a return value, `u32` type character then the value (`'t'` and an 8-byte
  object for a created resource).
- Flag `0x8`: a 32-byte block, `u32 3, u32 8`, then three u64 values that increase through
  a capture.
- Some records carry further blocks of the same shape (`u32 5, u32 8, u64`). The block
  types are not decoded yet.

## index (xdic)

| Offset | Field |
|---|---|
| 0x00 | `"xdic"`, u32 0, u32 bucketCount, u32 entryCount, u32 entryCount |
| 0x14 | bucketCount x `{u32 entry, u32 entry, u32 0xffffffff}`; empty buckets are all `0xffffffff` |
| | entryCount x `{u32 size, u32 compressedSize, u64 offset, u64 kind}` |
| | entryCount x u16 name length, including the NUL |
| | names, NUL-terminated, sorted |

Entry *i* has name *i*. Kind 1 is a blob: a zlib stream of `compressedSize` bytes at
`offset` in `store0`, inflating to `size` bytes. Kind 2 is a file in the bundle;
`compressedSize` then holds the entry's own index. Names include `U` keys, bundle file
names, and `(device info)`-style entries.

`store0` is the concatenation of those zlib streams.

## Texture data

`-10236` in `device-resources` snapshots a texture region:
`C@6ulululU<b>ululul` is texture, region (x y z w h d), mip level, slice, data name,
bytes per row, bytes per image, data length. The data is a bundle file or, when small, a
`store0` blob. `unused-device-resources` holds the same records for resources the captured
frame did not use.

Every texture data file (`MTLTexture-*`, `IOSurface-*`, `CAMetalLayer-*`) starts with a
header; pixels follow at the header size:

| Offset | Field |
|---|---|
| 0x00 | `"erutpac\0"` |
| 0x08 | u16 2, u16 1, u32 header size (`0x100`) |
| 0x10 | u64 1 |
| 0x18 | u64 MTLPixelFormat |
| 0x20 | u64 width, height, depth |
| 0x38 | u64 bytes per row, bytes per image |

Identical files are stored once; later names are symlinks to the first.

## Open

- Records per texture also include `-16310 CU` (creation, a key whose blob is ~3.7 KB),
  `-10231 CU` (a key many textures share), and integer properties `-10227`, `-10222`,
  `-10205`, `-10204`. `-16239` holds the owning process id. The texture descriptor has not
  been located; the pixel format comes from the data header instead.
- Draw calls, pipeline state and resource bindings in `capture` are not mapped to function
  ids yet.
