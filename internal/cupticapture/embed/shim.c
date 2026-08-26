/*
 * gputrace CUPTI capture shim.
 *
 * Injected into the target process via LD_PRELOAD by `gputrace capture`.
 * Arms CUPTI activity tracing in a constructor (CONCURRENT_KERNEL first,
 * serialized KERNEL as fallback; latency timestamps on), records kernels,
 * memcpys, memsets, and — behind GPUTRACE_CAPTURE_API — runtime/driver
 * API calls as newline-delimited JSON.
 *
 * Flushing happens on the application's own thread, from interposed CUDA
 * synchronization points (Device/Event/StreamSynchronize, Memcpy). The
 * destructor performs one FORCED flush only when no sync point ever fired
 * (driver-API-only apps); flushing after context teardown otherwise
 * deadlocks, so the common path never relies on it.
 *
 * Environment:
 *   GPUTRACE_CAPTURE_OUT   - output path for the JSONL event file (required;
 *                            actual file gets a .<pid>.jsonl suffix)
 *   GPUTRACE_APP_EVENTS    - optional sidecar path advertised to the target;
 *                            the shim does not read or write it
 *   GPUTRACE_CAPTURE_API   - enable runtime/driver API call records
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


static FILE *g_out = NULL;
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
     * "kernel is slow" from "kernel waited in the stream queue". */
    if (k->queued || k->submitted)
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

/* --- CUPTI callbacks ----------------------------------------------------- */

static void CUPTIAPI bufferRequested(uint8_t **buffer, size_t *size,
                                     size_t *maxNumRecords) {
    uint8_t *buf = (uint8_t *)malloc(BUFSIZE + ALIGN_SIZE);
    if (!buf) { *size = 0; *maxNumRecords = 0; return; }
    *buffer = buf;
    *size = BUFSIZE;
    *maxNumRecords = MAX_RECORDS;
    debug("buffer requested");
}

static void CUPTIAPI bufferCompleted(CUcontext ctx, uint32_t streamId,
                                     uint8_t *buffer, size_t size,
                                     size_t validSize) {
    (void)ctx; (void)streamId; (void)size;
    CUpti_Activity *record = NULL;
    if (getenv("GPUTRACE_CAPTURE_DEBUG"))
        fprintf(stderr, "gputrace-shim: buffer completed validSize=%zu\n", validSize);
    if (!g_out) return;
    pthread_mutex_lock(&g_lock);
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
            /* CUPTI_ERROR_MAX_LIMIT_REACHED here means "no more records
             * in this buffer"; anything else is also a stop condition. */
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
    if (getenv("GPUTRACE_CAPTURE_API")) {
        CUptiResult ar = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_RUNTIME);
        debug(ar == CUPTI_SUCCESS ? "runtime api records on" : "runtime api enable failed");
        ar = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_DRIVER);
        debug(ar == CUPTI_SUCCESS ? "driver api records on" : "driver api enable failed");
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
static volatile int g_flushed_once = 0;

__attribute__((destructor))
static void shim_shutdown(void) {
    g_enabled = 0;
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
