#!/bin/sh
# perfetto-ui-smoke.sh proves that a gputrace native trace becomes visible in
# an unmodified pinned Perfetto UI. Handler tests cover routing and the
# embedding handshake; this covers rendering, which they cannot.
#
# It serves the trace with `gputrace timeline --serve --ui-dir`, drives a
# headless Chromium over the DevTools protocol, and fails when the UI renders
# no tracks.
#
# Usage: tools/perfetto-ui-smoke.sh --ui-dir DIR trace.gputrace
#
# Environment:
#   GPUTRACE              gputrace binary (default: gputrace)
#   PERFETTO_UI_DIR       default --ui-dir
#   CHROME                Chromium-family browser binary
set -eu

ui=${PERFETTO_UI_DIR:-}
while [ "$#" -gt 0 ]; do
	case "$1" in
	--ui-dir)
		[ "$#" -ge 2 ] || { echo "--ui-dir requires a directory" >&2; exit 2; }
		ui=$2
		shift 2
		;;
	*)
		break
		;;
	esac
done
[ "$#" -eq 1 ] || { echo "usage: $0 --ui-dir DIR trace.gputrace" >&2; exit 2; }
trace=$1
[ -e "$trace" ] || { echo "trace not found: $trace" >&2; exit 2; }
[ -n "$ui" ] || { echo "a pinned Perfetto UI directory is required; see docs/PERFETTO_VIEWER_SPEC.md" >&2; exit 2; }
[ -f "$ui/perfetto-ui.json" ] || { echo "no perfetto-ui.json in $ui" >&2; exit 2; }

gputrace=${GPUTRACE:-gputrace}
command -v "$gputrace" >/dev/null || { echo "gputrace not found: $gputrace" >&2; exit 2; }
command -v node >/dev/null || { echo "node not found; it drives the DevTools protocol" >&2; exit 2; }

chrome=${CHROME:-}
if [ -z "$chrome" ]; then
	for candidate in \
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" \
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
		"/Applications/Chromium.app/Contents/MacOS/Chromium"; do
		[ -x "$candidate" ] && { chrome=$candidate; break; }
	done
fi
[ -n "$chrome" ] && [ -x "$chrome" ] || { echo "no Chromium-family browser found; set CHROME" >&2; exit 2; }

work=$(mktemp -d)
server_pid=
browser_pid=
cleanup() {
	[ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null
	[ -n "$browser_pid" ] && { kill "$browser_pid" 2>/dev/null; wait "$browser_pid" 2>/dev/null; }
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

port=9333
"$gputrace" timeline "$trace" --format perfetto --serve --ui-dir "$ui" \
	--listen 127.0.0.1:0 -o "$work/timeline.pftrace" > "$work/serve.log" 2>&1 &
server_pid=$!

url=
i=0
while [ "$i" -lt 120 ]; do
	url=$(sed -n 's|.*\(http://127\.0\.0\.1:[0-9]*\)/*.*|\1|p' "$work/serve.log" | head -1)
	[ -n "$url" ] && break
	kill -0 "$server_pid" 2>/dev/null || { echo "viewer exited:" >&2; cat "$work/serve.log" >&2; exit 1; }
	i=$((i + 1))
	sleep 1
done
[ -n "$url" ] || { echo "viewer did not print a URL:" >&2; cat "$work/serve.log" >&2; exit 1; }

"$chrome" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
	--user-data-dir="$work/profile" --remote-debugging-port="$port" \
	about:blank > "$work/browser.log" 2>&1 &
browser_pid=$!

i=0
while [ "$i" -lt 60 ]; do
	curl -fsS "http://127.0.0.1:$port/json/version" >/dev/null 2>&1 && break
	i=$((i + 1))
	sleep 1
done
curl -fsS "http://127.0.0.1:$port/json/version" >/dev/null 2>&1 ||
	{ echo "browser did not open a DevTools endpoint:" >&2; cat "$work/browser.log" >&2; exit 1; }

node "$(dirname "$0")/perfetto-ui-smoke.mjs" "$port" "$url/"
echo "Perfetto UI smoke gate passed: $trace rendered in $(basename "$ui")"
