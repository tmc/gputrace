/*
 * gputrace CUPTI capture shim.
 *
 * Injected into the target process via LD_PRELOAD by `gputrace capture`.
 * Arms CUPTI activity tracing in a constructor (CONCURRENT_KERNEL first,
 * serialized KERNEL as fallback; latency timestamps on), records kernels,
 * memcpys, memsets, and — behind GPUTRACE_CAPTURE_API — runtime/driver
 * API calls as newline-delimited JSON.
 *
 * Flushing happens two ways. Interposed CUDA synchronization points
 * (Device/Event/StreamSynchronize, Memcpy) flush on the application's own
 * thread, and a flush thread calls cuptiActivityFlushAll on a timer. The
 * destructor performs one FORCED flush only when no flush ever fired;
 * flushing after context teardown otherwise deadlocks, so the common path
 * never relies on it.
 *
 * The flush thread is what makes interposition-proof targets capturable
 * [V]. A target that links the CUDA runtime statically -- MLX's libmlx.so
 * carries cudaDeviceSynchronize and cudaLaunchKernel as local symbols --
 * makes those calls internally, where LD_PRELOAD cannot see them, and its
 * CUDA-graph launches reach the driver through cuGetProcAddress rather
 * than the PLT, so no driver entry point is interposable either. Before
 * any timed flush, capturing MLX yielded zero kernels while the workload
 * ran perfectly.
 *
 * The flush must be cuptiActivityFlushAll, not cuptiActivityFlushPeriod
 * [V]. FlushPeriod delivers only buffers that filled, so whatever sits in
 * a partial buffer when the target exits is lost -- roughly outstanding
 * buffers times the buffer size, which is why a bigger buffer loses more
 * and a 16 MiB one captures nothing at all. FlushPeriod does not move
 * that: on an MLX decode whose argmax must run 129 times, periods of 100,
 * 25 and 10 ms all recorded the same 102. Calling FlushAll on a timer
 * completes partial buffers and recovers them -- 127 at 10 ms -- and
 * makes the buffer size stop mattering. Non-forced is what does this; the
 * FORCED flag is the one that deadlocks, and is still confined to the
 * destructor.
 *
 * Environment:
 *   GPUTRACE_CAPTURE_OUT   - output path for the JSONL event file (required;
 *                            actual file gets a .<pid>.jsonl suffix)
 *   GPUTRACE_APP_EVENTS    - optional sidecar path advertised to the target;
 *                            the shim does not read or write it
 *   GPUTRACE_CAPTURE_API   - enable runtime/driver API call records
 *   GPUTRACE_CAPTURE_NVTX  - enable NVTX marker (range) records
 *   GPUTRACE_CAPTURE_FLUSH_MS - flush interval in ms (default 10; 0
 *                            disables the flush thread). Records still
 *                            unflushed when the target exits are lost, so
 *                            this bounds the loss.
 *   GPUTRACE_CAPTURE_BUFSIZE_MB - activity buffer size in MiB (default 4).
 *                            Rarely needs changing; see the note on
 *                            g_bufsize for why it exists
 *   GPUTRACE_CAPTURE_DEBUG - diagnostics on stderr
 *
 * Pure C: no runtime beyond libc and libcuda/libcupti. The Go parent never
 * injects a garbage collector into the traced process.
 */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include <unistd.h>
#include <fcntl.h>
#include <dlfcn.h>
#include <time.h>

#include <cuda.h>
#include <cupti.h>

#define BUFSIZE 4 * 1024 * 1024
#define ALIGN_SIZE 8
#define MAX_RECORDS 0

/* Activity buffer size, in bytes. Rarely worth touching now; the knob is
 * kept because it is what made the flush bug visible.
 *
 * While flushing went through cuptiActivityFlushPeriod, which does not
 * complete a PARTIAL buffer, records reached bufferCompleted only when a
 * buffer filled, so whatever was outstanding at exit was lost and the
 * loss was roughly outstanding buffers times this size. Smaller was
 * better, which is the opposite of the intuition, and that inversion is
 * how the cause was found. Measured on a GB10 MLX decode whose argmax
 * must run 129 times [V]: 1 MiB recovered 119, 2 MiB 117, this 4 MiB
 * default 102, and at 16 MiB no buffer filled inside the run at all, so
 * the capture came back empty while the shim reported itself armed.
 *
 * With the flush thread calling cuptiActivityFlushAll on an interval,
 * partial buffers come back and the size stops mattering: the same ladder
 * runs 127-128 at every size from 1 to 64 MiB [V]. The lever is
 * GPUTRACE_CAPTURE_FLUSH_MS. */
static size_t g_bufsize = BUFSIZE;


static FILE *g_out = NULL;
/* Set once any flush has succeeded. While it is still 0 at exit the app
 * never synchronized, and the destructor makes one forced attempt. */
static volatile int g_flushed_once = 0;

static pthread_mutex_t g_lock = PTHREAD_MUTEX_INITIALIZER;
static int g_enabled = 0;

static void debug(const char *msg) {
    if (getenv("GPUTRACE_CAPTURE_DEBUG")) {
        fprintf(stderr, "gputrace-shim: %s\n", msg);
    }
}


/* --- JSON escaping ------------------------------------------------------ */

static void json_escape(FILE *out, const char *s) {
    fputc('"', out);
    for (; *s; s++) {
        unsigned char c = (unsigned char)*s;
        switch (c) {
        case '"': fputs("\\\"", out); break;
        case '\\': fputs("\\\\", out); break;
        case '\n': fputs("\\n", out); break;
        case '\r': fputs("\\r", out); break;
        case '\t': fputs("\\t", out); break;
        default:
            if (c < 0x20) fprintf(out, "\\u%04x", c);
            else fputc(c, out);
        }
    }
    fputc('"', out);
}



/* --- record emitters ------------------------------------------------------ */

static const char *memory_kind_name(uint8_t kind) {
    switch (kind) {
    case CUPTI_ACTIVITY_MEMORY_KIND_UNKNOWN:       return "unknown";
    case CUPTI_ACTIVITY_MEMORY_KIND_PAGEABLE:      return "pageable";
    case CUPTI_ACTIVITY_MEMORY_KIND_PINNED:        return "pinned";
    case CUPTI_ACTIVITY_MEMORY_KIND_DEVICE:        return "device";
    case CUPTI_ACTIVITY_MEMORY_KIND_ARRAY:         return "array";
    case CUPTI_ACTIVITY_MEMORY_KIND_MANAGED:       return "managed";
    case CUPTI_ACTIVITY_MEMORY_KIND_DEVICE_STATIC: return "device-static";
    case CUPTI_ACTIVITY_MEMORY_KIND_MANAGED_STATIC:return "managed-static";
    default:                                       return "other";
    }
}

/* Cbid names for the runtime/driver calls that matter for launch-overhead
 * analysis. CUPTI does not expose a numeric->name table in the activity API,
 * so we carry the small set that dominates inference workloads and fall
 * back to the number for everything else. */
static const char *cbid_name(uint32_t cbid) {
    switch (cbid) {
    /* runtime (cupti_runtime_cbid.h) */
    case 211: return "cudaLaunchKernel";
    case 212: return "cudaLaunchKernelExC";
    case 223: return "cudaMemcpyAsync";
    case 218: return "cudaMemcpy";
    case 222: return "cudaMemsetAsync";
    case 231: return "cudaStreamSynchronize";
    case 229: return "cudaDeviceSynchronize";
    case 217: return "cudaEventSynchronize";
    case 251: return "cudaEventRecord";
    case 206: return "cudaMallocManaged";
    /* driver (cupti_driver_cbid.h) */
    case 307: return "cuLaunchKernel";
    case 308: return "cuLaunchKernelEx";
    case 402: return "cuMemcpyHtoDAsync_v2";
    case 405: return "cuMemcpyDtoHAsync_v2";
    default:  return NULL;
    }
}

static void emit_api(const CUpti_ActivityAPI *a, const char *api_kind) {
    const char *name = cbid_name(a->cbid);
    fprintf(g_out, "{\"kind\":\"api\",\"api\":");
    json_escape(g_out, api_kind);
    if (name) {
        fprintf(g_out, ",\"name\":");
        json_escape(g_out, name);
    }
    fprintf(g_out,
        ",\"cbid\":%u,\"start_ns\":%llu,\"end_ns\":%llu,"
        "\"thread_id\":%u,\"correlation_id\":%llu}\n",
        a->cbid,
        (unsigned long long)a->start, (unsigned long long)a->end,
        a->threadId, (unsigned long long)a->correlationId);
}



static void emit_kernel(CUpti_ActivityKernel4 *k) {
    /* No demangling here: this runs on a CUPTI callback thread where fork
     * (which c++filt needs) can deadlock against CUDA's internal locks.
     * The raw symbol is recorded; gputrace demangles at read time. */
    const char *name = k->name ? (const char *)k->name : "";
    fprintf(g_out,
        "{\"kind\":\"kernel\",\"raw_symbol\":");
    json_escape(g_out, name);
    fprintf(g_out,
        ",\"start_ns\":%llu,\"end_ns\":%llu,"
        "\"grid\":\"%ux%ux%u\",\"block\":\"%ux%ux%u\","
        "\"registers\":%d,"
        "\"shared_mem\":%d,\"local_mem_per_thread\":%u,"
        "\"context_id\":%u,\"device_id\":%u,\"stream_id\":%u,\"correlation_id\":%llu",
        (unsigned long long)k->start, (unsigned long long)k->end,
        k->gridX, k->gridY, k->gridZ, k->blockX, k->blockY, k->blockZ,
        (int)k->registersPerThread,
        (int)(k->staticSharedMemory + k->dynamicSharedMemory),
        k->localMemoryPerThread,
        k->contextId, k->deviceId, k->streamId, (unsigned long long)k->correlationId);
    /* Kernel4 lacks graphId/graphNodeId; they live in Kernel8+ at a
     * fixed offset past the prefix-compatible region. The record buffer
     * is large enough for the newest struct CUPTI emits, so reading
     * Kernel9 fields through the same pointer is safe on CUDA >= 11.8
     * and reads garbage (guarded by !=0) nowhere else. */
    {
        const CUpti_ActivityKernel9 *k9 = (const CUpti_ActivityKernel9 *)k;
        if (k9->graphId != 0)
            fprintf(g_out, ",\"graph_id\":%u,\"graph_node_id\":%llu",
                    k9->graphId, (unsigned long long)k9->graphNodeId);
    }
    /* Latency timestamps (enabled at arm time): queued/submitted separate
     * "kernel is slow" from "kernel waited in the stream queue".
     *
     * Emit them only as a consistent triple. CUPTI writes
     * CUPTI_TIMESTAMP_UNKNOWN (0) for launches it cannot time, notably
     * CUDA-graph nodes, and a record buffer reused across activities can
     * carry a stale nonzero queued while submitted stays 0. Emitting on
     * "either is nonzero" published that stale value: on a GB10 MLX
     * capture 45,943 of 46,138 kernels shared one identical queued
     * timestamp, implying a 1.16 s launch latency that never happened. */
    if (k->queued != CUPTI_TIMESTAMP_UNKNOWN &&
        k->submitted != CUPTI_TIMESTAMP_UNKNOWN &&
        k->queued <= k->submitted && k->submitted <= k->start)
        fprintf(g_out, ",\"queued_ns\":%llu,\"submitted_ns\":%llu",
                (unsigned long long)k->queued, (unsigned long long)k->submitted);
    fprintf(g_out, "}\n");
}

static void emit_memcpy(CUpti_ActivityMemcpy5 *m) {
    const char *kind;
    switch (m->copyKind) {
    case CUPTI_ACTIVITY_MEMCPY_KIND_HTOD: kind = "HtoD"; break;
    case CUPTI_ACTIVITY_MEMCPY_KIND_DTOH: kind = "DtoH"; break;
    case CUPTI_ACTIVITY_MEMCPY_KIND_DTOD: kind = "DtoD"; break;
    default: kind = "other";
    }
    fprintf(g_out,
        "{\"kind\":\"memcpy\",\"dir\":");
    json_escape(g_out, kind);
    /* src/dst memory kinds turn "transfers rival compute" into "and X%
     * moved pageable memory — pin these buffers". */
    fprintf(g_out,
        ",\"src_kind\":");
    json_escape(g_out, memory_kind_name(m->srcKind));
    fprintf(g_out, ",\"dst_kind\":");
    json_escape(g_out, memory_kind_name(m->dstKind));
    fprintf(g_out,
        ",\"start_ns\":%llu,\"end_ns\":%llu,\"bytes\":%llu,"
        "\"device_id\":%u,\"stream_id\":%u,\"correlation_id\":%llu}\n",
        (unsigned long long)m->start, (unsigned long long)m->end,
        (unsigned long long)m->bytes,
        m->deviceId, m->streamId, (unsigned long long)m->correlationId);
}

static void emit_memset(CUpti_ActivityMemset3 *m) {
    fprintf(g_out,
        "{\"kind\":\"memset\",\"start_ns\":%llu,\"end_ns\":%llu,\"bytes\":%llu,"
        "\"device_id\":%u,\"stream_id\":%u}\n",
        (unsigned long long)m->start, (unsigned long long)m->end,
        (unsigned long long)m->bytes, m->deviceId, m->streamId);
}

/* NVTX ranges arrive as MARKER records: a start and an end sharing one id,
 * with the name carried on the start. They are emitted raw and paired at
 * read time — pairing in the shim would need per-thread state on a CUPTI
 * callback thread, and an unpaired start is still evidence. */
static void emit_marker(const CUpti_ActivityMarker2 *m) {
    const char *flag = "other";
    if (m->flags & CUPTI_ACTIVITY_FLAG_MARKER_START) flag = "start";
    else if (m->flags & CUPTI_ACTIVITY_FLAG_MARKER_END) flag = "end";
    fprintf(g_out, "{\"kind\":\"marker\",\"phase\":");
    json_escape(g_out, flag);
    if (m->name) {
        fprintf(g_out, ",\"name\":");
        json_escape(g_out, (const char *)m->name);
    }
    if (m->domain) {
        fprintf(g_out, ",\"domain\":");
        json_escape(g_out, (const char *)m->domain);
    }
    fprintf(g_out, ",\"marker_id\":%u,\"timestamp_ns\":%llu}\n",
            m->id, (unsigned long long)m->timestamp);
}

/* Flush thread. cuptiActivityFlushAll(0) completes buffers that have not
 * filled, which is the only way records leave a target that never reaches
 * an interposed synchronization point. It runs non-forced: the FORCED flag
 * is what deadlocks against a live context, and it stays in the destructor.
 *
 * Detached and never joined, because the targets this exists for do not run
 * destructors -- a Go binary exits through exit_group -- so there is no
 * shutdown path to join it from. It touches only CUPTI, which is safe to
 * call from any thread, and stops on a flag when a destructor does run. */
static volatile int g_flush_stop = 0;

static void *flush_thread(void *arg) {
    unsigned long ms = (unsigned long)(uintptr_t)arg;
    while (!g_flush_stop) {
        usleep((useconds_t)(ms * 1000));
        if (g_flush_stop) break;
        cuptiActivityFlushAll(0);
    }
    return NULL;
}

/* --- CUPTI callbacks ----------------------------------------------------- */

static void CUPTIAPI bufferRequested(uint8_t **buffer, size_t *size,
                                     size_t *maxNumRecords) {
    uint8_t *buf = (uint8_t *)malloc(g_bufsize + ALIGN_SIZE);
    if (!buf) { *size = 0; *maxNumRecords = 0; return; }
    *buffer = buf;
    *size = g_bufsize;
    *maxNumRecords = MAX_RECORDS;
    debug("buffer requested");
}

static void CUPTIAPI bufferCompleted(CUcontext ctx, uint32_t streamId,
                                     uint8_t *buffer, size_t size,
                                     size_t validSize) {
    (void)size;
    CUpti_Activity *record = NULL;
    if (getenv("GPUTRACE_CAPTURE_DEBUG"))
        fprintf(stderr, "gputrace-shim: buffer completed validSize=%zu\n", validSize);
    if (!g_out) return;
    pthread_mutex_lock(&g_lock);
    /* Records CUPTI discarded because no buffer was free when they were
     * produced. Uniform, deterministic loss spread across a whole run is
     * what this looks like from the outside: on an MLX decode it silently
     * removed ~48% of every kernel, and every count and total derived from
     * that capture was confidently wrong. The count is emitted per buffer
     * rather than summed at exit because the destructor does not run for a
     * target that exits through exit_group -- the same reason the capture
     * can be silently empty. */
    {
        size_t dropped = 0;
        if (cuptiActivityGetNumDroppedRecords(ctx, streamId, &dropped) == CUPTI_SUCCESS &&
            dropped > 0) {
            fprintf(g_out, "{\"kind\":\"dropped\",\"records\":%llu,\"stream_id\":%u}\n",
                    (unsigned long long)dropped, streamId);
        }
    }
    while (1) {
        CUptiResult status = cuptiActivityGetNextRecord(buffer, validSize, &record);
        if (status == CUPTI_SUCCESS) {
            switch (record->kind) {
            case CUPTI_ACTIVITY_KIND_CONCURRENT_KERNEL:
                emit_kernel((CUpti_ActivityKernel4 *)record);
                break;
            case CUPTI_ACTIVITY_KIND_KERNEL:
                emit_kernel((CUpti_ActivityKernel4 *)record);
                break;
            case CUPTI_ACTIVITY_KIND_MEMCPY:
                emit_memcpy((CUpti_ActivityMemcpy5 *)record);
                break;
            case CUPTI_ACTIVITY_KIND_MEMSET:
                emit_memset((CUpti_ActivityMemset3 *)record);
                break;
            case CUPTI_ACTIVITY_KIND_MARKER:
                emit_marker((const CUpti_ActivityMarker2 *)record);
                break;
            case CUPTI_ACTIVITY_KIND_RUNTIME:
                emit_api((const CUpti_ActivityAPI *)record, "runtime");
                break;
            case CUPTI_ACTIVITY_KIND_DRIVER:
                emit_api((const CUpti_ActivityAPI *)record, "driver");
                break;
            default:
                break;
            }
        } else {
            /* CUPTI_ERROR_MAX_LIMIT_REACHED means "no more records in this
             * buffer" and is the normal end. Any other status stops the
             * loop with records still in the buffer, so it is a silent
             * partial read of a buffer that arrived intact -- loss that
             * cuptiActivityGetNumDroppedRecords does not report, because
             * from CUPTI's side nothing was dropped. */
            if (status != CUPTI_ERROR_MAX_LIMIT_REACHED &&
                getenv("GPUTRACE_CAPTURE_DEBUG"))
                fprintf(stderr, "gputrace-shim: record walk stopped early rc=%d\n", (int)status);
            break;
        }
    }
    fflush(g_out);
    free(buffer);
    pthread_mutex_unlock(&g_lock);
}

/* --- init / teardown ------------------------------------------------------ */

/* Flush after each synchronize by intercepting the CUDA runtime API.
 * This keeps flushing on the application's own thread, which avoids the
 * CUPTI cross-thread deadlock seen with a dedicated flush thread. */

static void arm_cupti(void) {
    CUptiResult r;
    {
        const char *bs = getenv("GPUTRACE_CAPTURE_BUFSIZE_MB");
        if (bs && *bs) {
            char *end = NULL;
            unsigned long mb = strtoul(bs, &end, 10);
            if (end && *end == 0 && mb > 0 && mb <= 1024)
                g_bufsize = (size_t)mb * 1024 * 1024;
        }
    }
    /* Resolve CUPTI lazily: the shim is LD_PRELOADed and may load before
     * libcupti is on the process's symbol map. dlopen the known paths. */
    void *h = dlopen("libcupti.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!h) h = dlopen("libcupti.so.13", RTLD_LAZY | RTLD_GLOBAL);
    if (!h) h = dlopen("libcupti.so.12", RTLD_LAZY | RTLD_GLOBAL);
    int i;
    if (!h) {
        for (i = 0; i < 8 && !h; i++) {
            char p[256];
            snprintf(p, sizeof(p), "/usr/local/cuda%d/lib64/libcupti.so", 13 - i / 4);
            h = dlopen(p, RTLD_LAZY | RTLD_GLOBAL);
            if (!h) h = dlopen("/usr/local/cuda/lib64/libcupti.so", RTLD_LAZY | RTLD_GLOBAL);
        }
    }
    if (!h) { debug("cannot dlopen libcupti"); return; }
    r = cuptiActivityRegisterCallbacks(bufferRequested, bufferCompleted);
    if (r != CUPTI_SUCCESS) { debug("register failed"); return; }
    /* CONCURRENT_KERNEL first: serialized KERNEL serializes all launches
     * and hides real stream overlap, distorting the profile. Only fall
     * back to KERNEL when CONCURRENT is refused (rare, old drivers). */
    int concurrent = 0;
    r = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_CONCURRENT_KERNEL);
    if (r == CUPTI_SUCCESS) {
        concurrent = 1;
        debug("using concurrent-kernel activity");
    } else {
        if (getenv("GPUTRACE_CAPTURE_DEBUG"))
            fprintf(stderr, "gputrace-shim: enable concurrent-kernel rc=%d\n", (int)r);
        r = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_KERNEL);
        if (r == CUPTI_SUCCESS)
            debug("concurrent unavailable; using serialized kernel activity");
        else if (getenv("GPUTRACE_CAPTURE_DEBUG"))
            fprintf(stderr, "gputrace-shim: enable kernel rc=%d\n", (int)r);
    }
    /* Latency timestamps: per-launch queued/submitted times, which separate
     * "kernel is slow" from "kernel waited in the stream queue". */
    cuptiActivityEnableLatencyTimestamps(1);
    cuptiActivityEnable(CUPTI_ACTIVITY_KIND_MEMCPY);
    cuptiActivityEnable(CUPTI_ACTIVITY_KIND_MEMSET);
    /* Runtime/driver API records: host-side call timing per launch. These
     * are what turn "GPU was idle 35ms/token" into "each launch costs X us
     * of host time". Gated behind env because they multiply record volume. */
    /* NVTX ranges: the only source of application semantics ("decode
     * token 47") that needs no cooperation from us beyond arming it. The
     * capture command also sets NVTX_INJECTION64_PATH so a target that
     * loads NVTX dynamically routes into CUPTI. */
    if (getenv("GPUTRACE_CAPTURE_NVTX")) {
        CUptiResult nr = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_MARKER);
        debug(nr == CUPTI_SUCCESS ? "nvtx markers on" : "nvtx marker enable failed");
    }
    if (getenv("GPUTRACE_CAPTURE_API")) {
        CUptiResult ar = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_RUNTIME);
        debug(ar == CUPTI_SUCCESS ? "runtime api records on" : "runtime api enable failed");
        ar = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_DRIVER);
        debug(ar == CUPTI_SUCCESS ? "driver api records on" : "driver api enable failed");
    }
    /* Timed flush: the one mechanism that needs nothing from the target.
     * A target that links the CUDA runtime statically and launches through
     * CUDA graphs -- MLX -- makes no interposable call at all in steady
     * state, so without this the capture ends empty while the workload runs
     * perfectly.
     *
     * The period bounds the loss rather than eliminating it: whatever has
     * not been flushed when the target exits is gone, and the destructor
     * cannot recover it because a Go target exits through exit_group and
     * never runs one. Measured on an MLX decode whose argmax must run 129
     * times [V]: 100ms recorded 106, 50ms 122, 25ms 123, 10ms 127. Cost is
     * below the noise -- every captured run finished inside the spread of
     * the uncaptured control -- so the default is the short end. */
    {
        unsigned long period_ms = 10;
        const char *p = getenv("GPUTRACE_CAPTURE_FLUSH_MS");
        if (p && *p) {
            char *end = NULL;
            unsigned long v = strtoul(p, &end, 10);
            if (end && *end == 0) period_ms = v;
        }
        if (period_ms > 0) {
            pthread_t th;
            int rc = pthread_create(&th, NULL, flush_thread,
                                    (void *)(uintptr_t)period_ms);
            if (rc == 0) pthread_detach(th);
            if (getenv("GPUTRACE_CAPTURE_DEBUG"))
                fprintf(stderr, "gputrace-shim: flush thread %lums rc=%d\n", period_ms, rc);
        }
    }
    g_enabled = 1;
    if (g_out) {
        fprintf(g_out, "{\"kind\":\"capture_meta\",\"concurrent_kernel\":%s,\"pid\":%d}\n",
                concurrent ? "true" : "false", (int)getpid());
        /* Clock sync: CUPTI's timestamp domain is undocumented relative to
         * wall clock. Record both at one instant so the reader can align
         * NVML samples (CLOCK_REALTIME) even if the domains diverge. */
        uint64_t cupti_now = 0;
        cuptiGetTimestamp(&cupti_now);
        struct timespec ts;
        clock_gettime(CLOCK_REALTIME, &ts);
        fprintf(g_out, "{\"kind\":\"clock_sync\",\"unix_ns\":%llu,\"cupti_ns\":%llu}\n",
                (unsigned long long)ts.tv_sec * 1000000000ull + (unsigned long long)ts.tv_nsec,
                (unsigned long long)cupti_now);
        fflush(g_out);
    }
    debug("armed");
}

__attribute__((constructor))
static void shim_init(void) {
    const char *path = getenv("GPUTRACE_CAPTURE_OUT");
    if (!path || !*path) return; // not ours; stay inert

    /* Per-PID output: LD_PRELOAD and GPUTRACE_CAPTURE_OUT are inherited by
     * children (torchrun ranks, dataloader workers, python -m), and a
     * shared stdio FILE* interleaves mid-line. Each process writes its own
     * events.<pid>.jsonl; readers merge on timestamp. */
    if (g_out) { fclose(g_out); g_out = NULL; }

    char with_pid[1024];
    snprintf(with_pid, sizeof(with_pid), "%s.%d.jsonl", path, (int)getpid());
    g_out = fopen(with_pid, "a");
    if (!g_out && getenv("GPUTRACE_CAPTURE_DEBUG"))
        fprintf(stderr, "gputrace-shim: cannot open %s\n", with_pid);
    if (!g_out) { debug("cannot open output"); return; }

    arm_cupti();
}

/* Records whether any interposed sync point has flushed successfully. When
 * it is still 0 at exit, the app never synchronized: buffered records would
 * otherwise be lost to context teardown. In that case we make ONE
 * best-effort forced flush from the destructor. On hosts where this
 * deadlocks (observed on GB10 when a CUDA context was alive), the process
 * was exiting anyway and the per-sync-point flushes already captured the
 * bulk of records; on driver-API-only apps (no cudaDeviceSynchronize calls)
 * this path is what recovers the capture at all. */

__attribute__((destructor))
static void shim_shutdown(void) {
    g_enabled = 0;
    g_flush_stop = 1;
    if (!g_flushed_once && g_out) {
        /* No sync-point flush ever ran. Try once; CUPTI's context may be
         * gone, in which case this returns an error rather than hanging
         * (verified: driver-API-only apps). */
        cuptiActivityFlushAll(CUPTI_ACTIVITY_FLAG_FLUSH_FORCED);
        /* Brief drain window for the completed-buffer callback. */
        usleep(100 * 1000);
    }
    if (g_out) { fflush(g_out); fclose(g_out); g_out = NULL; }
    debug("shutdown complete");
}

/* --- CUDA runtime API interposition ------------------------------------- */

cudaError_t cudaDeviceSynchronize(void);

static cudaError_t (*real_cudaDeviceSynchronize)(void) = NULL;

cudaError_t cudaDeviceSynchronize(void) {
    if (!real_cudaDeviceSynchronize) {
        real_cudaDeviceSynchronize = dlsym(RTLD_NEXT, "cudaDeviceSynchronize");
    }
    cudaError_t err = real_cudaDeviceSynchronize();
    if (g_enabled && g_out) {
        cuptiActivityFlushAll(0);
        fflush(g_out);
        g_flushed_once = 1;
    }
    return err;
}

typedef int (*cudaEventSyncFn)(void *);

static void flush_if_enabled(void) {
    if (g_enabled && g_out) {
        cuptiActivityFlushAll(0);
        fflush(g_out);
        g_flushed_once = 1;
    }
}

cudaError_t cudaEventSynchronize(cudaEvent_t event) {
    static cudaError_t (*real)(cudaEvent_t) = NULL;
    if (!real) real = (cudaError_t (*)(cudaEvent_t))dlsym(RTLD_NEXT, "cudaEventSynchronize");
    cudaError_t err = real(event);
    flush_if_enabled();
    return err;
}

cudaError_t cudaStreamSynchronize(cudaStream_t stream) {
    static cudaError_t (*real)(cudaStream_t) = NULL;
    if (!real) real = (cudaError_t (*)(cudaStream_t))dlsym(RTLD_NEXT, "cudaStreamSynchronize");
    cudaError_t err = real(stream);
    flush_if_enabled();
    return err;
}

cudaError_t cudaMemcpy(void *dst, const void *src, size_t count, enum cudaMemcpyKind kind) {
    static cudaError_t (*real)(void *, const void *, size_t, enum cudaMemcpyKind) = NULL;
    if (!real) real = (cudaError_t (*)(void *, const void *, size_t, enum cudaMemcpyKind))dlsym(RTLD_NEXT, "cudaMemcpy");
    cudaError_t err = real(dst, src, count, kind);
    flush_if_enabled();
    return err;
}
