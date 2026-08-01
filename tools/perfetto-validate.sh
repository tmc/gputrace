#!/bin/bash
# perfetto-validate.sh checks the clock-domain contract of the reference
# capture's two Perfetto exports.
#
# Usage: tools/perfetto-validate.sh busy.json wall.json
#
# The expected counts are intentionally pinned to
# qwen25-05b-python-producer-tokens1-3-perfdata.gputrace. They are an
# integration guard for the exporter, not a generic trace-format rule.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: $0 busy.json wall.json" >&2; exit 2; }
BUSY="$1"
WALL="$2"
TP="${TRACE_PROCESSOR_SHELL:-$HOME/tmp/trace_processor_shell}"

[ -x "$TP" ] || { echo "trace_processor_shell not found: $TP" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq not found" >&2; exit 2; }

count_events() {
	local file="$1" filter="$2"
	jq -r ".traceEvents | map(select($filter)) | length" "$file"
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
want_count "busy encoders" "$(count_events "$BUSY" '.cat == "encoder"')" 21
want_count "busy dispatches" "$(count_events "$BUSY" '.cat == "kernel"')" 864
want_count "busy non-contained dispatches" "$(count_events "$BUSY" '.cat == "kernel" and .args.encoder_containment == "not_strictly_contained"')" 108
want_count "busy wall-clock events" "$(count_events "$BUSY" '.cat == "command_buffer" or .cat == "encoder_profile" or .cat == "gprwcntr"')" 0

check_parser "$WALL"
want_count "wall command buffers" "$(count_events "$WALL" '.cat == "command_buffer"')" 30
want_count "wall busy events" "$(count_events "$WALL" '.cat == "encoder" or .cat == "kernel" or .cat == "counter"')" 0

echo "Perfetto clock-domain validation passed"
