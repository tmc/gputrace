# GPU Shader Profiling - Quick Reference

## Complete Workflow (3 Steps)

### 1. Capture GPU Trace
```bash
cd examples/mlx-lm-go/models
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=3x
```
**Output**: `/tmp/forward_pass_*.gputrace`

### 2. Convert to Pprof
```bash
cd experiments/gputrace
go build -o /tmp/gputrace2pprof ./cmd/gputrace2pprof
/tmp/gputrace2pprof /tmp/forward_pass_*.gputrace -all -prefix gpu_profile
```
**Output**: `gpu_profile.gpu.pprof.gz`, `gpu_profile.txt`, etc.

### 3. Analyze
```bash
go tool pprof -top gpu_profile.gpu.pprof.gz
go tool pprof -http=:8080 gpu_profile.gpu.pprof.gz
```

## One-Liner Demo
```bash
cd experiments/gputrace && ./demo_shader_pprof.sh
```

## Common Commands

### gputrace2pprof

| Command | Description |
|---------|-------------|
| `gputrace2pprof trace.gputrace` | Convert to pprof (default: trace.pprof.gz) |
| `gputrace2pprof trace.gputrace -o out.pprof.gz` | Custom output path |
| `gputrace2pprof trace.gputrace -all -prefix analysis` | Generate all formats |
| `gputrace2pprof trace.gputrace -v` | Verbose output with summary |
| `gputrace2pprof trace.gputrace -text -o report.txt` | Text report only |

### go tool pprof

| Command | Description |
|---------|-------------|
| `go tool pprof -top file.pprof.gz` | Top functions by time |
| `go tool pprof -http=:8080 file.pprof.gz` | Interactive web UI |
| `go tool pprof -list=shader_name file.pprof.gz` | Details for specific shader |
| `go tool pprof -tree file.pprof.gz` | Hierarchical tree view |
| `go tool pprof -base=old.pprof.gz new.pprof.gz` | Compare two profiles |

## Expected Output

### Good (Swift-like)
```
Showing nodes accounting for 2.87ms, 100% of total
      flat  flat%   sum%        cum   cum%
   1.21ms 42.16% 42.16%    1.21ms 42.16%  affine_qmv_fast_float16...
   0.43ms 14.98% 57.14%    0.43ms 14.98%  rope_single_freqs_float16
   0.38ms 13.24% 70.38%    0.38ms 13.24%  steel_attention_...
```
✅ No separate dequantization
✅ Using qmv (matrix-vector) for single tokens
✅ Low total GPU time

### Bad (Current Go)
```
Showing nodes accounting for 51.19ms, 100% of total
      flat  flat%   sum%        cum   cum%
  23.08ms 45.10% 45.10%   23.08ms 45.10%  affine_qmm_t_float16...
  14.67ms 28.66% 73.76%   14.67ms 28.66%  affine_qmv_fast_float16...
  10.55ms 20.62% 94.38%   10.55ms 20.62%  affine_dequantize_float16...
```
❌ Separate dequantization (20.62% overhead)
❌ Using qmm_t (matrix-matrix) instead of qmv
❌ High total GPU time (17.8x slower than Swift)

## Key Performance Issues to Look For

| Issue | Symptom in Pprof | Fix Needed |
|-------|-----------------|------------|
| **Separate Dequantization** | `affine_dequantize_*` shader present | Fuse with matmul |
| **Wrong Kernel Type** | `affine_qmm_t_*_batch_0` for single tokens | Use `affine_qmv` instead |
| **Excessive SIMD Groups** | 8x more SIMD groups than Swift | Fix dispatch logic |
| **Too Many Encoders** | 45 encoders vs Swift's 22 | Improve batching |

## File Organization

```
experiments/gputrace/
├── cmd/
│   ├── gputrace2pprof/    - Main conversion tool
│   └── analyze/           - Basic analysis tool
├── SHADER_PPROF_GUIDE.md  - Complete guide
├── QUICK_REFERENCE.md     - This file
├── demo_shader_pprof.sh   - Demo script
└── *.go                   - Library code
```

## Integration with Benchmarks

Add to your benchmark:
```go
func BenchmarkForwardPass(b *testing.B) {
	bench := gpuprof.New(b, "forward_pass")
	defer bench.Close()

	// ... benchmark code ...
}
```

Run with GPU capture:
```bash
MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$
```

This automatically:
- Captures GPU trace to `/tmp/forward_pass_*.gputrace`
- Can be converted to pprof with `gputrace2pprof`

## Troubleshooting

**No .gputrace file generated?**
- Ensure `MTL_CAPTURE_ENABLED=1` is set
- Check `/tmp/` directory
- Verify benchmark actually runs GPU operations

**Empty timing data?**
- Some .gputrace formats don't include timing
- Tool will use synthetic timing for visualization
- Kernel names and order are still accurate

**xctrace export fails?**
- This is expected and normal
- `gputrace2pprof` parses binary format directly
- No need for xctrace

## See Also

- [SHADER_PPROF_GUIDE.md](./SHADER_PPROF_GUIDE.md) - Detailed usage guide
- [GPU_TRACE_FORMAT.md](./GPU_TRACE_FORMAT.md) - File format documentation
- [../gpuprof/SHADER_ANALYSIS_GUIDE.md](../gpuprof/SHADER_ANALYSIS_GUIDE.md) - Performance analysis from DBD3
