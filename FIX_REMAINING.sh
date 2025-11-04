#!/bin/bash
# Script to fix remaining issues in gputrace refactoring

set -e

echo "Fixing undefined types and constants..."

# Fix RecordType constants - add to trace package
cat >> internal/trace/trace.go << 'EOF'

// Record type constants for MTSP records
const (
	RecordTypeCt RecordType = 'C' // 'C' followed by 't'
	RecordTypeCi RecordType = 'C' // 'C' followed by 'i'
)
EOF

# Fix helper functions - add trace. prefix where they're defined in trace
for file in internal/command/count.go internal/command/dispatch.go internal/analysis/device.go; do
    sed -i '' 's/undefined: RecordTypeCt/trace.RecordTypeCt/g; s/undefined: RecordTypeCi/trace.RecordTypeCi/g' "$file" 2>/dev/null || true
    sed -i '' 's/\bRecordTypeCt\b/trace.RecordTypeCt/g; s/\bRecordTypeCi\b/trace.RecordTypeCi/g' "$file"
done

# Fix MagicMTSP
sed -i '' 's/\bMagicMTSP\b/trace.MagicMTSP/g' internal/analysis/device.go

# Fix helper method calls that are now in same package
for pkg in internal/buffer internal/command internal/analysis; do
    for file in $pkg/*.go; do
        # Convert t.helper(...) to helper(t, ...) for private helpers
        sed -i '' 's/t\.\(detect[A-Z][a-zA-Z]*\)(/\1(t, /g; s/t\.\(build[A-Z][a-zA-Z]*\)(/\1(t, /g; s/t\.\(parse[A-Z][a-zA-Z]*\)(/\1(t, /g; s/t\.\(populate[A-Z][a-zA-Z]*\)(/\1(t, /g' "$file"
    done
done

# Fix undefined Trace - should be *trace.Trace
sed -i '' 's/\*Trace\>/*trace.Trace/g' internal/counter/export.go internal/counter/sampling.go internal/replay/*.go internal/shader/*.go internal/analysis/*.go internal/timing/*.go

# Fix ComputeEncoder
sed -i '' 's/\bComputeEncoder\b/trace.ComputeEncoder/g' internal/counter/export.go

# Run goimports to fix all imports
goimports -w -local github.com/tmc/mlx-go/experiments/gputrace internal/

echo "Testing build..."
if go build ./internal/...; then
    echo "✅ Internal packages build successfully!"
else
    echo "❌ Still have errors. Running detailed check..."
    go build ./internal/... 2>&1 | head -50
fi

echo "
Next steps:
1. Fix any remaining undefined errors manually
2. Update cmd/gputrace to use internal packages
3. Run full test suite: go test ./...
4. Commit final working state
"
