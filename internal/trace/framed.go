package trace

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Record flag bits (FramedRecord.Flags).
const (
	FlagReturnValue = 0x1 // a return value follows the arguments
	FlagTrailer     = 0x8 // a 32-byte trailer ends the record
)

const (
	framedHeaderSize = 0x24
	trailerSize      = 32
)

// FramedRecord is one call in an MTSP stream, read by its length prefix.
// Layout: u32 size, i32 function id, 24 reserved bytes, u32 flags,
// NUL-terminated signature padded to 4 bytes, then the arguments.
type FramedRecord struct {
	Offset    int
	FuncID    int32
	Flags     uint32
	Signature string
	Body      []byte
}

// WalkFramedRecords splits an MTSP file into records. Unlike ParseMTSPFromData it does
// not scan for markers: the file is "MTSP", a u32 version, then records back to back.
func WalkFramedRecords(data []byte) ([]FramedRecord, error) {
	if len(data) < 8 || string(data[:4]) != MagicMTSP {
		return nil, fmt.Errorf("not an MTSP stream")
	}
	var recs []FramedRecord
	for off := 8; off < len(data); {
		if off+framedHeaderSize > len(data) {
			return recs, fmt.Errorf("truncated record header at %#x", off)
		}
		size := int(binary.LittleEndian.Uint32(data[off:]))
		if size < framedHeaderSize || off+size > len(data) {
			return recs, fmt.Errorf("bad record size %d at %#x", size, off)
		}
		rec := data[off : off+size]
		end := bytes.IndexByte(rec[framedHeaderSize:], 0)
		if end < 0 {
			return recs, fmt.Errorf("unterminated signature at %#x", off)
		}
		bodyStart := framedHeaderSize + (end+4)/4*4
		if bodyStart > size {
			bodyStart = size
		}
		recs = append(recs, FramedRecord{
			Offset:    off,
			FuncID:    int32(binary.LittleEndian.Uint32(rec[4:])),
			Flags:     binary.LittleEndian.Uint32(rec[0x20:]),
			Signature: string(rec[framedHeaderSize : framedHeaderSize+end]),
			Body:      rec[bodyStart:],
		})
		off += size
	}
	return recs, nil
}

// Arg is one decoded argument.
type Arg struct {
	Type  string
	Uint  uint64
	Float float64
	Str   string
}

func (a Arg) String() string {
	switch a.Type {
	case "S", "U":
		return strconv.Quote(a.Str)
	case "f", "d":
		return strconv.FormatFloat(a.Float, 'g', -1, 64)
	case "C", "t":
		return fmt.Sprintf("%#x", a.Uint)
	case "i", "l", "c", "s":
		return strconv.FormatInt(signExtend(a.Uint, typeSize[a.Type]), 10)
	case "<b>":
		return "<bytes>"
	}
	return strconv.FormatUint(a.Uint, 10)
}

func signExtend(v uint64, size int) int64 {
	shift := 64 - 8*size
	return int64(v<<shift) >> shift
}

// typeSize holds the fixed-size signature tokens. S and U are NUL-terminated strings
// padded to 4 bytes; <b> marks bytes stored outside the record (a file or store0 key).
var typeSize = map[string]int{
	"C": 8, "t": 8, "ul": 8, "l": 8, "uw": 8, "w": 8, "d": 8,
	"i": 4, "ui": 4, "f": 4,
	"us": 2, "s": 2,
	"uc": 1, "c": 1, "b": 1,
	"<b>": 0,
}

// SignatureTokens splits a signature such as "C@6ulululU<b>ululul" into tokens;
// "@6ul" is an array of six "ul".
func SignatureTokens(sig string) ([]string, error) {
	var toks []string
	for i := 0; i < len(sig); {
		count := ""
		if sig[i] == '@' {
			j := i + 1
			for j < len(sig) && sig[j] >= '0' && sig[j] <= '9' {
				j++
			}
			count, i = sig[i:j], j
		}
		tok := ""
		for _, t := range []string{"<b>", "ul", "uw", "ui", "us", "uc"} {
			if strings.HasPrefix(sig[i:], t) {
				tok = t
				break
			}
		}
		if tok == "" && i < len(sig) {
			tok = sig[i : i+1]
		}
		if tok == "" {
			return nil, fmt.Errorf("signature %q ends after %q", sig, count)
		}
		if _, ok := typeSize[tok]; !ok && tok != "S" && tok != "U" {
			return nil, fmt.Errorf("unknown token %q in %q", tok, sig)
		}
		toks = append(toks, count+tok)
		i += len(tok)
	}
	return toks, nil
}

// Args decodes the record's arguments. It returns the arguments and the bytes after
// them: the return value when FlagReturnValue is set, then the trailer when
// FlagTrailer is set.
func (r FramedRecord) Args() ([]Arg, []byte, error) {
	toks, err := SignatureTokens(r.Signature)
	if err != nil {
		return nil, nil, err
	}
	b, pos := r.Body, 0
	var args []Arg
	for _, tok := range toks {
		n := 1
		if tok[0] == '@' {
			j := 1
			for tok[j] >= '0' && tok[j] <= '9' {
				j++
			}
			n, _ = strconv.Atoi(tok[1:j])
			tok = tok[j:]
		}
		for k := 0; k < n; k++ {
			if tok == "S" || tok == "U" {
				end := bytes.IndexByte(b[min(pos, len(b)):], 0)
				if end < 0 {
					return args, nil, fmt.Errorf("unterminated string argument")
				}
				args = append(args, Arg{Type: tok, Str: string(b[pos : pos+end])})
				pos = (pos + end + 4) / 4 * 4
				continue
			}
			size := typeSize[tok]
			if size >= 4 {
				pos = (pos + 3) / 4 * 4
			}
			if pos+size > len(b) {
				return args, nil, fmt.Errorf("argument %q runs past the record", tok)
			}
			a := Arg{Type: tok}
			switch size {
			case 8:
				a.Uint = binary.LittleEndian.Uint64(b[pos:])
			case 4:
				a.Uint = uint64(binary.LittleEndian.Uint32(b[pos:]))
			case 2:
				a.Uint = uint64(binary.LittleEndian.Uint16(b[pos:]))
			case 1:
				a.Uint = uint64(b[pos])
			}
			switch tok {
			case "f":
				a.Float = float64(math.Float32frombits(uint32(a.Uint)))
			case "d":
				a.Float = math.Float64frombits(a.Uint)
			}
			args = append(args, a)
			pos += size
		}
	}
	pos = (pos + 3) / 4 * 4
	return args, b[min(pos, len(b)):], nil
}

// Trailer returns the three u64 values of the 32-byte trailer (u32 3, u32 8, 3 x u64),
// present when FlagTrailer is set. They increase through a capture, like timestamps.
func (r FramedRecord) Trailer() ([3]uint64, bool) {
	var v [3]uint64
	if r.Flags&FlagTrailer == 0 || len(r.Body) < trailerSize {
		return v, false
	}
	t := r.Body[len(r.Body)-trailerSize:]
	for i := range v {
		v[i] = binary.LittleEndian.Uint64(t[8+8*i:])
	}
	return v, true
}
