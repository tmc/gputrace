#!/bin/sh
# perfetto-native-validate.sh checks a native gputrace Perfetto export.
set -eu

require_gpu=false
sql=
while [ "$#" -gt 0 ]; do
	case "$1" in
	--require-gpu)
		require_gpu=true
		shift
		;;
	--sql)
		[ "$#" -ge 2 ] || { echo "--sql requires a file" >&2; exit 2; }
		sql=$2
		shift 2
		;;
	*)
		break
		;;
	esac
done
if [ "$#" -ne 1 ]; then
	echo "usage: $0 [--require-gpu] [--sql gputrace.sql] trace.pftrace" >&2
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

manifest_schema=$(
	"$tp" query "$trace" \
		"select extract_arg(arg_set_id,'debug.schema') from slice where name='gputrace evidence manifest'" \
		2>/dev/null | tail -1 | tr -d '"'
)
[ "$manifest_schema" = gputrace.perfetto/v1 ] || {
	echo "native trace has no gputrace.perfetto/v1 manifest" >&2
	exit 1
}

if [ -n "$sql" ]; then
	[ -f "$sql" ] || { echo "PerfettoSQL file not found: $sql" >&2; exit 2; }
	view_dispatches=$(
		{ sed -n '1,$p' "$sql"; printf '%s\n' 'select count(*) from gputrace_dispatch;'; } |
			"$tp" query "$trace" 2>/dev/null | tail -1 | tr -d '"'
	)
	[ "$view_dispatches" = "$gpu_slices" ] || {
		echo "gputrace_dispatch has $view_dispatches rows; gpu_slice has $gpu_slices" >&2
		exit 1
	}
fi

printf 'native Perfetto validation passed: %s slices, %s GPU slices\n' "$slices" "$gpu_slices"
