package cmd

import (
	"strings"
	"testing"
)

func TestDiffOptionsValidate(t *testing.T) {
	base := diffOptions{
		Limit:        20,
		MinDeltaUs:   0,
		DivergenceUs: 20,
		OnlyEncoder:  -1,
		BenchDir:     "/tmp/bench",
	}

	tests := []struct {
		name    string
		opts    diffOptions
		args    []string
		wantErr string
	}{
		{
			name: "valid bench dir",
			opts: base,
		},
		{
			name: "valid positional pair",
			opts: diffOptions{
				Limit:        20,
				MinDeltaUs:   0,
				DivergenceUs: 20,
				OnlyEncoder:  -1,
			},
			args: []string{"left.gputrace", "right.gputrace"},
		},
		{
			name: "valid explicit pair override",
			opts: diffOptions{
				Limit:        20,
				MinDeltaUs:   0,
				DivergenceUs: 20,
				OnlyEncoder:  -1,
				BenchDir:     "/tmp/bench",
				Left:         "left.gputrace",
				Right:        "right.gputrace",
			},
		},
		{
			name: "json and csv conflict",
			opts: func() diffOptions {
				o := base
				o.JSON = true
				o.CSV = true
				return o
			}(),
			wantErr: "--json and --csv are mutually exclusive",
		},
		{
			name: "invalid limit",
			opts: func() diffOptions {
				o := base
				o.Limit = 0
				return o
			}(),
			wantErr: "--limit must be > 0",
		},
		{
			name: "invalid min delta",
			opts: func() diffOptions {
				o := base
				o.MinDeltaUs = -1
				return o
			}(),
			wantErr: "--min-delta-us must be >= 0",
		},
		{
			name: "invalid divergence threshold",
			opts: func() diffOptions {
				o := base
				o.DivergenceUs = 0
				return o
			}(),
			wantErr: "--divergence-threshold-us must be > 0",
		},
		{
			name: "invalid only encoder",
			opts: func() diffOptions {
				o := base
				o.OnlyEncoder = -2
				return o
			}(),
			wantErr: "--only-encoder must be >= -1",
		},
		{
			name: "invalid by value",
			opts: func() diffOptions {
				o := base
				o.By = "bogus"
				return o
			}(),
			wantErr: "invalid --by value",
		},
		{
			name: "pipeline pairs by value allowed",
			opts: func() diffOptions {
				o := base
				o.By = "pipeline-pairs"
				return o
			}(),
		},
		{
			name:    "single positional arg rejected",
			opts:    base,
			args:    []string{"left-only.gputrace"},
			wantErr: "expected 0 or 2 positional traces, got 1",
		},
		{
			name: "left without right rejected",
			opts: func() diffOptions {
				o := base
				o.Left = "left.gputrace"
				return o
			}(),
			wantErr: "--left and --right must be provided together",
		},
		{
			name: "positional with explicit pair rejected",
			opts: func() diffOptions {
				o := base
				o.Left = "left.gputrace"
				o.Right = "right.gputrace"
				return o
			}(),
			args:    []string{"a.gputrace", "b.gputrace"},
			wantErr: "positional traces cannot be combined with --left/--right",
		},
		{
			name:    "positional with bench dir rejected",
			opts:    base,
			args:    []string{"a.gputrace", "b.gputrace"},
			wantErr: "positional traces cannot be combined with --bench-dir",
		},
		{
			name: "csv requires single by",
			opts: func() diffOptions {
				o := base
				o.CSV = true
				o.By = "function,dispatch"
				return o
			}(),
			wantErr: "--csv requires a single --by view",
		},
		{
			name: "csv with text only flags rejected",
			opts: func() diffOptions {
				o := base
				o.CSV = true
				o.ShowMatches = true
				return o
			}(),
			wantErr: "--csv cannot be combined with text-only flags",
		},
		{
			name: "quick json allowed",
			opts: func() diffOptions {
				o := base
				o.Quick = true
				o.JSON = true
				return o
			}(),
		},
		{
			name: "divergence with encoder by allowed",
			opts: func() diffOptions {
				o := base
				o.By = "encoder"
				o.Divergence = true
				return o
			}(),
		},
		{
			name: "divergence without encoder by rejected",
			opts: func() diffOptions {
				o := base
				o.Divergence = true
				return o
			}(),
			wantErr: "--divergence requires --by encoder",
		},
		{
			name: "quick with by rejected",
			opts: func() diffOptions {
				o := base
				o.Quick = true
				o.By = "function"
				return o
			}(),
			wantErr: "--quick cannot be combined with --by",
		},
		{
			name: "quick with show flags rejected",
			opts: func() diffOptions {
				o := base
				o.Quick = true
				o.ShowUnmatched = true
				return o
			}(),
			wantErr: "--quick cannot be combined with --show-matches/--show-unmatched/--show-occurrences/--explain",
		},
		{
			name: "by encoder with by rejected",
			opts: func() diffOptions {
				o := base
				o.ByEncoder = true
				o.By = "encoder"
				return o
			}(),
			wantErr: "--by-encoder cannot be combined with --by",
		},
		{
			name: "json with text only flags rejected",
			opts: func() diffOptions {
				o := base
				o.JSON = true
				o.Explain = true
				return o
			}(),
			wantErr: "--json cannot be combined with text-only flags",
		},
		{
			name: "missing inputs rejected",
			opts: diffOptions{
				Limit:        20,
				MinDeltaUs:   0,
				DivergenceUs: 20,
				OnlyEncoder:  -1,
			},
			wantErr: "missing traces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate(tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestResolveDiffInputsExplicitOverrideNote(t *testing.T) {
	opts := diffOptions{
		Left:     "left.gputrace",
		Right:    "right.gputrace",
		BenchDir: "/tmp/bench",
	}

	left, right, note, err := resolveDiffInputs(nil, opts)
	if err != nil {
		t.Fatalf("resolveDiffInputs returned error: %v", err)
	}
	if left != "left.gputrace" || right != "right.gputrace" {
		t.Fatalf("unexpected pair: left=%q right=%q", left, right)
	}
	if !strings.Contains(note, "--bench-dir ignored") {
		t.Fatalf("expected bench-dir override note, got %q", note)
	}
}
