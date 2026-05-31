package cmd

import (
	"path/filepath"
	"testing"
)

func TestValidateDumpRecordsFlags(t *testing.T) {
	tests := []struct {
		name    string
		offset  int
		limit   int
		wantErr string
	}{
		{
			name:   "default",
			offset: 0,
			limit:  -1,
		},
		{
			name:   "zero limit",
			offset: 0,
			limit:  0,
		},
		{
			name:   "positive",
			offset: 3,
			limit:  10,
		},
		{
			name:    "negative offset",
			offset:  -1,
			limit:   -1,
			wantErr: "--offset must be >= 0",
		},
		{
			name:    "limit below unlimited sentinel",
			offset:  0,
			limit:   -2,
			wantErr: "--limit must be >= -1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDumpRecordsFlags(tt.offset, tt.limit)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateDumpRecordsFlags(%d, %d): %v", tt.offset, tt.limit, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateDumpRecordsFlags(%d, %d) succeeded, want error", tt.offset, tt.limit)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRunDumpRecordsValidatesFlagsBeforeTraceIO(t *testing.T) {
	tests := []struct {
		name    string
		offset  int
		limit   int
		wantErr string
	}{
		{
			name:    "negative offset",
			offset:  -1,
			limit:   -1,
			wantErr: "--offset must be >= 0",
		},
		{
			name:    "limit below unlimited sentinel",
			offset:  0,
			limit:   -2,
			wantErr: "--limit must be >= -1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldOffset := dumpRecordsOffset
			oldLimit := dumpRecordsLimit
			dumpRecordsOffset = tt.offset
			dumpRecordsLimit = tt.limit
			t.Cleanup(func() {
				dumpRecordsOffset = oldOffset
				dumpRecordsLimit = oldLimit
			})

			missingTrace := filepath.Join(t.TempDir(), "missing.gputrace")
			err := runDumpRecords(nil, []string{missingTrace})
			if err == nil {
				t.Fatal("runDumpRecords succeeded, want error")
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
