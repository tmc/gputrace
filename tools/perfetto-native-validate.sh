#!/bin/sh
# perfetto-native-validate.sh checks a native gputrace Perfetto export.
set -eu

require_gpu=false
if [ "${1:-}" = "--require-gpu" ]; then
	require_gpu=true
	shift
fi
if [ "$#" -ne 1 ]; then
	echo "usage: $0 [--require-gpu] trace.pftrace" >&2
	exit 2
fi

tp=${TRACE_PROCESSOR_SHELL:-$HOME/tmp/trace_processor_shell}
[ -x "$tp" ] || { echo "trace_processor_shell not found: $tp" >&2; exit 2; }

trace=$1
[ -f "$trace" ] || { echo "trace not found: $trace" >&2; exit 2; }

errors=$(
	"$tp" query "$trace" \
		"select coalesce(sum(value),0) from stats where value>0 and severity in ('error','data_loss')" \
		2>/dev/null | tail -1 | tr -d '"'
)
[ "$errors" = 0 ] || { echo "trace processor reported $errors errors or losses" >&2; exit 1; }

gpu_slices=$(
	"$tp" query "$trace" "select count(*) from gpu_slice" 2>/dev/null |
		tail -1 | tr -d '"'
)
if "$require_gpu"; then
	[ "$gpu_slices" -gt 0 ] || { echo "native trace contains no gpu_slice rows" >&2; exit 1; }
fi

slices=$(
	"$tp" query "$trace" "select count(*) from slice" 2>/dev/null |
		tail -1 | tr -d '"'
)
[ "$slices" -gt 0 ] || { echo "native trace contains no slice rows" >&2; exit 1; }

printf 'native Perfetto validation passed: %s slices, %s GPU slices\n' "$slices" "$gpu_slices"
