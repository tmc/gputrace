#!/bin/bash
# perfetto-validate.sh checks the clock-domain contract of two Perfetto
# exports. It can additionally check the event counts of the reference
# capture.
#
# Usage: tools/perfetto-validate.sh [--reference-counts] busy.json wall.json
#
# The expected counts are intentionally pinned to
# ~/tmp/gputrace-captures/qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata2.gputrace.
# They are an
# integration guard for the exporter, not a generic trace-format rule, so
# running this against any other capture fails on the counts rather than on
# anything being wrong.
#
# Produce the two inputs with:
#
#   T=~/tmp/gputrace-captures/qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata2.gputrace
#   gputrace timeline "$T" --format perfetto --clock busy -o busy.json
#   gputrace timeline "$T" --format perfetto --clock wall -o wall.json
set -euo pipefail

REFERENCE_COUNTS=false
if [ "${1:-}" = "--reference-counts" ]; then
	REFERENCE_COUNTS=true
	shift
fi

[ $# -eq 2 ] || { echo "usage: $0 [--reference-counts] busy.json wall.json" >&2; exit 2; }
BUSY="$1"
WALL="$2"
TP="${TRACE_PROCESSOR_SHELL:-$HOME/tmp/trace_processor_shell}"

[ -x "$TP" ] || { echo "trace_processor_shell not found: $TP" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq not found" >&2; exit 2; }

count_events() {
	local file="$1" filter="$2"
	jq -r ".traceEvents | map(select($filter)) | length" "$file"
}

count_containment_tid_mismatches() {
	local file="$1"
	jq -r '
		(reduce .traceEvents[] as $event ({};
			if $event.cat == "encoder" and ($event.args.index? != null)
			then .[($event.args.index | tostring)] = $event.tid
			else .
			end
		)) as $encoder_tid |
		[.traceEvents[] |
			select(.cat == "kernel" and .args.encoder_containment == "strict") |
			select(.tid != $encoder_tid[(.args.encoder_index | tostring)])
		] | length
	' "$file"
}

want_count() {
	local name="$1" got="$2" want="$3"
	if [ "$got" != "$want" ]; then
		echo "$name = $got, want $want" >&2
		exit 1
	fi
	echo "$name = $got"
}

check_parser() {
	local file="$1" failures
	failures="$("$TP" query "$file" "SELECT COALESCE(SUM(value), 0) AS failures FROM stats WHERE name IN ('json_tokenizer_failure', 'json_parser_failure', 'flow_no_enclosing_slice');" 2>&1 | sed -n '/^"failures"/,$p' | tail -1)"
	if [ "$failures" != "0" ]; then
		echo "trace_processor parser failures for $file = ${failures:-missing}" >&2
		exit 1
	fi
	echo "parser failures $(basename "$file") = 0"
}

check_parser "$BUSY"
want_count "busy contained dispatches on encoder tracks" "$(count_containment_tid_mismatches "$BUSY")" 0
want_count "busy wall-clock events" "$(count_events "$BUSY" '.cat == "command_buffer" or .cat == "profiler_stream" or .cat == "gprwcntr"')" 0

check_parser "$WALL"
want_count "wall busy events" "$(count_events "$WALL" '.cat == "encoder" or .cat == "kernel" or .cat == "counter"')" 0
want_count "wall raw profiler events" "$(count_events "$WALL" '.cat == "profiler_stream" or .cat == "gprwcntr"')" 0

if "$REFERENCE_COUNTS"; then
	want_count "busy encoders" "$(count_events "$BUSY" '.cat == "encoder"')" 23
	want_count "busy dispatches" "$(count_events "$BUSY" '.cat == "kernel"')" 958
	want_count "busy non-contained dispatches" "$(count_events "$BUSY" '.cat == "kernel" and .args.encoder_containment == "not_strictly_contained"')" 117
	want_count "wall command buffers" "$(count_events "$WALL" '.cat == "command_buffer"')" 24
fi

echo "Perfetto clock-domain validation passed"
