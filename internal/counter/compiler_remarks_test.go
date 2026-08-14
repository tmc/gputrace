package counter

import "testing"

func TestParseCompilerRemarks(t *testing.T) {
	raw := `--- !Passed
Pass:            loop-unroll
Name:            FullyUnrolled
DebugLoc:        { File: '/tmp/a:b/it''s.h',
                   Line: 325, Column: 0 }
Function:        agc.main
Args:
  - String:          'completely unrolled loop with '
  - UnrollCount:     '2'
  - String:          ' iterations'
...
--- !Missed
Pass:            regalloc
Name:            SpillReloadCopies
DebugLoc:        { File: 'broken.h', Line: 4
Function:        agc.main
...
--- !Analysis
Pass:            asm-printer
Name:            InstructionCount
Function:        agc.main.constant_program
...
--- !Analysis
Pass:            asm-printer
Name:            InstructionMix
DebugLoc:        { File: 'kernel.metal', Line: 0, Column: 0 }
Function:        agc.main
...
`
	remarks := ParseCompilerRemarks(raw)
	if len(remarks) != 4 {
		t.Fatalf("remarks = %d, want 4", len(remarks))
	}
	first := remarks[0]
	if first.Index != 0 || first.Kind != "Passed" || first.Pass != "loop-unroll" ||
		first.Name != "FullyUnrolled" || first.Function != "agc.main" ||
		first.SourceFile != "/tmp/a:b/it's.h" || first.SourceLine == nil || *first.SourceLine != 325 ||
		first.SourceColumn == nil || *first.SourceColumn != 0 || first.ParseStatus != "complete" {
		t.Fatalf("first remark = %#v", first)
	}
	if len(first.Arguments) != 3 || first.Arguments[1].Index != 1 ||
		first.Arguments[1].Name != "UnrollCount" || first.Arguments[1].RawValue != "'2'" ||
		first.Arguments[1].Value == nil || *first.Arguments[1].Value != "2" ||
		first.Arguments[1].ParseStatus != "complete" {
		t.Fatalf("first arguments = %#v", first.Arguments)
	}
	malformed := remarks[1]
	if malformed.Index != 1 || malformed.ParseStatus != "malformed" ||
		malformed.SourceFile != "" || malformed.SourceLine != nil || malformed.SourceColumn != nil {
		t.Fatalf("malformed remark = %#v", malformed)
	}
	last := remarks[2]
	if last.Index != 2 || last.Kind != "Analysis" || last.ParseStatus != "no_source_location" ||
		last.Pass != "asm-printer" || last.Name != "InstructionCount" ||
		last.Function != "agc.main.constant_program" {
		t.Fatalf("last remark = %#v", last)
	}
	unresolved := remarks[3]
	if unresolved.ParseStatus != "unresolved_source_location" || unresolved.SourceFile != "kernel.metal" ||
		unresolved.SourceLine == nil || *unresolved.SourceLine != 0 ||
		unresolved.SourceColumn == nil || *unresolved.SourceColumn != 0 {
		t.Fatalf("unresolved remark = %#v", unresolved)
	}
}

func TestParseCompilerRemarksPreservesEmptyAndMalformedArguments(t *testing.T) {
	raw := `--- !Analysis
Pass: asm-printer
Name: InstructionMix
Function: agc.main
Args:
  - BasicBlock:      ''
  - String:          "\n"
  - String:          >
    continued
...
`
	remarks := ParseCompilerRemarks(raw)
	if len(remarks) != 1 || len(remarks[0].Arguments) != 4 {
		t.Fatalf("remarks = %#v", remarks)
	}
	arguments := remarks[0].Arguments
	if arguments[0].Value == nil || *arguments[0].Value != "" ||
		arguments[1].Value == nil || *arguments[1].Value != "\n" {
		t.Fatalf("decoded arguments = %#v", arguments[:2])
	}
	for _, index := range []int{2, 3} {
		if arguments[index].ParseStatus != "malformed" || arguments[index].Value != nil {
			t.Fatalf("argument %d = %#v", index, arguments[index])
		}
	}
}

func TestParseCompilerRemarksMalformedHeaderDoesNotAbort(t *testing.T) {
	raw := `not yaml
--- !Passed
Pass: loop-unroll
Name: FullyUnrolled
Function: agc.main
...
`
	remarks := ParseCompilerRemarks(raw)
	if len(remarks) != 1 || remarks[0].Index != 0 || remarks[0].ParseStatus != "no_source_location" {
		t.Fatalf("remarks = %#v", remarks)
	}
}

func TestParseCompilerRemarksIgnoresNestedDebugLocation(t *testing.T) {
	raw := `--- !Analysis
Pass: asm-printer
Name: InstructionCount
Function: agc.main
Args:
  - DebugLoc: { File: 'not-the-document-location.metal', Line: 9, Column: 2 }
...
`
	remarks := ParseCompilerRemarks(raw)
	if len(remarks) != 1 || remarks[0].ParseStatus != "no_source_location" || remarks[0].SourceFile != "" {
		t.Fatalf("remarks = %#v", remarks)
	}
}
