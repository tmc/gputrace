#!/bin/bash
# nlm-sync-corpus.sh stages the gputrace evidence corpus and syncs it to a
# NotebookLM notebook under one stable name.
#
# The name carries no date and no variant suffix on purpose. `nlm source sync`
# replaces sources by name, so a stable name means re-running this updates the
# existing source set in place. Dated names ("... (2026-08-01)") and ad-hoc
# variants ("... - Full") each mint a *new* set, which is how the notebook ended
# up holding the same 50 MB twice.
#
# The corpus is not the repo. `repo: gputrace` already gives the notebook our
# source code; this gives it the evidence, none of which is in git:
#
#   probe-output/    what the probes printed against a real capture
#   command-output/  what every gputrace command emits for that capture
#   oracle/          Xcode's own counter-tab export, the parity reference
#   collab/          working notes, including retracted claims
#   perfetto/        the timeline artifact under critique
#   session-history  the transcript, with the reasoning behind each conclusion
#
# Usage:
#   tools/nlm-sync-corpus.sh <notebook-id> [capture.gputrace]
#   tools/nlm-sync-corpus.sh --prune <notebook-id>   # drop superseded sets
#
# Requires: nlm, gputrace on PATH (make reinstall), a readable capture.

set -euo pipefail

# SOURCE_NAME is the contract with the notebook. Changing it orphans the old
# set rather than replacing it, so do not add a date or a qualifier.
SOURCE_NAME="gputrace corpus"
STAGE="$HOME/tmp/gputrace-nlm-corpus"

# Titles this script has used before. Pruning matches on these so a stray
# unrelated source is never deleted by a broad pattern.
LEGACY_PREFIXES=(
	"gputrace Complete Staged Corpus"
	"gputrace Documentation, Research & Perfetto Parity Sources"
)

die() { echo "nlm-sync-corpus: $*" >&2; exit 1; }

prune() {
	local nb="$1" ids=() line id title
	while IFS=$'\t' read -r id title _; do
		[ "$id" = "ID" ] && continue
		for p in "${LEGACY_PREFIXES[@]}"; do
			case "$title" in "$p"*) ids+=("$id"); echo "  drop: $title";; esac
		done
	done < <(nlm source list "$nb")

	[ ${#ids[@]} -eq 0 ] && { echo "nothing to prune"; return 0; }
	printf '%s\n' "${ids[@]}" | nlm source delete -y "$nb" -
	echo "pruned ${#ids[@]} sources"
}

[ $# -ge 1 ] || die "usage: $0 [--prune] <notebook-id> [capture.gputrace]"

if [ "$1" = "--prune" ]; then
	shift
	[ $# -ge 1 ] || die "--prune needs a notebook id"
	prune "$1"
	exit 0
fi

NOTEBOOK="$1"
CAPTURE="${2:-$HOME/tmp/qwen25-05b-python-producer-tokens1-3-perfdata.gputrace}"
[ -e "$CAPTURE" ] || die "capture not found: $CAPTURE"

command -v nlm >/dev/null || die "nlm not on PATH"
command -v gputrace >/dev/null || die "gputrace not on PATH (make reinstall)"

REPO="$(cd "$(dirname "$0")/.." && pwd)"
PROFDIR="$(find -L "$CAPTURE" -maxdepth 1 -name '*.gpuprofiler_raw' -type d | head -1)"
[ -n "$PROFDIR" ] || die "no .gpuprofiler_raw inside $CAPTURE"

rm -rf "$STAGE"
mkdir -p "$STAGE"/{probes,probe-output,docs,collab,oracle,perfetto,command-output}
echo "$CAPTURE" > "$STAGE/CAPTURE_PATH.txt"

echo "staging sources..."
( cd "$REPO" && git ls-files | grep -E '_manual_test\.go|probe.*\.go' ) | while read -r f; do
	cp "$REPO/$f" "$STAGE/probes/${f//\//_}"
done
cp -r "$REPO"/docs/*.md "$REPO"/docs/research "$STAGE/docs/" 2>/dev/null || true
cp "$HOME"/tmp/agent-collab/gputrace/*.md "$STAGE/collab/" 2>/dev/null || true
cp "$HOME"/tmp/gputrace-xcode-oracle-*/*.txt "$STAGE/oracle/" 2>/dev/null || true

# The transcript is the largest and most useful single source: it carries the
# reasoning behind each conclusion AND each retraction. nlm chunks it at 5 MB.
for h in "$HOME"/.claude/projects/*gputrace/*.jsonl; do
	[ -e "$h" ] && cp "$h" "$STAGE/session-history-$(basename "$h" .jsonl).jsonl"
done

echo "running probes..."
GPUTRACE_PROBE_STREAMDATA="$PROFDIR" go -C "$REPO" test -v ./internal/counter \
	-run 'TestStreamDataTimebaseProbe|TestAPSTimelineKeysProbe' \
	> "$STAGE/probe-output/timebase-and-apstimeline.txt" 2>&1 || true
for f in 4 12 39; do
	[ -e "$PROFDIR/Counters_f_$f.raw" ] || continue
	GPUTRACE_PROBE_COUNTERS="$PROFDIR/Counters_f_$f.raw" go -C "$REPO" test -v ./internal/agxps \
		-run TestCounterFileParse > "$STAGE/probe-output/counterfile-$f.txt" 2>&1 || true
done
GPUTRACE_PROBE_COUNTERS_DIR="$PROFDIR" go -C "$REPO" test -v ./internal/agxps \
	-run TestCounterAggregate > "$STAGE/probe-output/counter-aggregate.txt" 2>&1 || true
go -C "$REPO" test -v ./internal/agxps -run TestCounterTableEnumerate \
	> "$STAGE/probe-output/counter-table-enumerate.txt" 2>&1 || true

echo "running commands..."
# brief and admit take two traces and are skipped deliberately; command-output
# README records that rather than leaving a silent hole.
for c in stats profiler timing shaders kernels encoders command-buffers tree \
	correlate insights api-calls buffers buffer-access dependencies fences \
	counters xcode-parity graph dump export-counters; do
	timeout 300 gputrace "$c" "$CAPTURE" > "$STAGE/command-output/$c.txt" 2>&1 || true
done
timeout 600 gputrace pprof "$CAPTURE" -o "$STAGE/command-output/profile.pb.gz" \
	> "$STAGE/command-output/pprof.txt" 2>&1 || true

echo "exporting timeline..."
# --format perfetto matters: without it this writes the *text* rendering, and a
# critique of "our Perfetto JSON" then reads prose instead of trace events.
gputrace timeline "$CAPTURE" --format perfetto -o "$STAGE/perfetto/timeline-perfetto.json" >/dev/null 2>&1 || true
gputrace timeline "$CAPTURE" --format text -o "$STAGE/perfetto/timeline-text.txt" >/dev/null 2>&1 || true

cp "$REPO/tools/nlm-corpus-README.md" "$STAGE/README.md" 2>/dev/null || true
cp "$REPO/tools/nlm-corpus-SUPERSEDED.md" "$STAGE/SUPERSEDED.md" 2>/dev/null || true

echo "syncing $(find "$STAGE" -type f | wc -l | tr -d ' ') files as \"$SOURCE_NAME\"..."
( cd "$STAGE" && nlm source sync --name "$SOURCE_NAME" "$NOTEBOOK" . )
echo "done. prune superseded sets with: $0 --prune $NOTEBOOK"
