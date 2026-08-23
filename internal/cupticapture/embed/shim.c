/*
 * gputrace CUPTI capture shim.
 *
 * Injected into the target process via LD_PRELOAD by `gputrace capture`.
 * Arms CUPTI activity tracing in a constructor, records kernel/memcpy/
 * memset launches as newline-delimited JSON, and flushes on exit.
 *
 * Environment:
 *   GPUTRACE_CAPTURE_OUT  - output path for the JSONL event file (required)
 *   GPUTRACE_CAPTURE_DEBUG- set to see diagnostics on stderr
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

static const char *demangle_cached(const char *mangled);

/* --- record emission ---------------------------------------------------- */

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
        "\"device_id\":%u,\"stream_id\":%u,\"correlation_id\":%llu}\n",
        (unsigned long long)k->start, (unsigned long long)k->end,
        k->gridX, k->gridY, k->gridZ, k->blockX, k->blockY, k->blockZ,
        (int)k->registersPerThread,
        k->deviceId, k->streamId, (unsigned long long)k->correlationId);
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

/* --- demangling via c++filt --------------------------------------------- */

#define DMANGLE_SLOTS 512
static struct { char *raw; char *pretty; } g_dmangle[DMANGLE_SLOTS];
static int g_dmangle_n = 0;

static const char *demangle_cached(const char *mangled) {
    int i;
    for (i = 0; i < g_dmangle_n; i++)
        if (strcmp(g_dmangle[i].raw, mangled) == 0)
            return g_dmangle[i].pretty;

    char cmd[1024];
    snprintf(cmd, sizeof(cmd), "c++filt %s", mangled);
    FILE *p = popen(cmd, "r");
    char pretty[512] = "";
    if (p) {
        size_t n = fread(pretty, 1, sizeof(pretty) - 2, p);
        while (n > 0 && (pretty[n-1] == '\n' || pretty[n-1] == ' ')) n--;
        pretty[n] = '\0';
        pclose(p);
    }
    if (pretty[0] == '\0' || strchr(pretty, ' ') == pretty /* failure modes */)
        strncpy(pretty, mangled, sizeof(pretty) - 1);

    char *raw_copy = strdup(mangled), *pretty_copy = strdup(pretty);
    if (!raw_copy || !pretty_copy) return mangled;
    if (g_dmangle_n >= DMANGLE_SLOTS) { /* recycle oldest slot */
        free(g_dmangle[0].raw); free(g_dmangle[0].pretty);
        memmove(&g_dmangle[0], &g_dmangle[1], sizeof(g_dmangle[0]) * (DMANGLE_SLOTS - 1));
        g_dmangle_n--;
    }
    g_dmangle[g_dmangle_n].raw = raw_copy;
    g_dmangle[g_dmangle_n].pretty = pretty_copy;
    g_dmangle_n++;
    return pretty_copy;
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
    r = cuptiActivityEnable(CUPTI_ACTIVITY_KIND_KERNEL);
    if (getenv("GPUTRACE_CAPTURE_DEBUG"))
        fprintf(stderr, "gputrace-shim: enable kernel rc=%d\n", (int)r);
    if (r != CUPTI_SUCCESS) {
        if (getenv("GPUTRACE_CAPTURE_DEBUG"))
            fprintf(stderr, "gputrace-shim: enable kernel rc=%d\n", (int)r);
        else {
            /* serialized KERNEL unsupported; try CONCURRENT as fallback */
            if (cuptiActivityEnable(CUPTI_ACTIVITY_KIND_CONCURRENT_KERNEL) == CUPTI_SUCCESS)
                debug("using concurrent-kernel activity");
        }
    } else {
        debug("using serialized kernel activity");
    }
    cuptiActivityEnable(CUPTI_ACTIVITY_KIND_MEMCPY);
    cuptiActivityEnable(CUPTI_ACTIVITY_KIND_MEMSET);
    g_enabled = 1;
    debug("armed");
}

__attribute__((constructor))
static void shim_init(void) {
    const char *path = getenv("GPUTRACE_CAPTURE_OUT");
    if (!path || !*path) return; // not ours; stay inert

    /* Skip our own compiler children (c++filt spawns are short-lived). */
    g_out = fopen(path, "a");
    if (!g_out) { debug("cannot open output"); return; }

    arm_cupti();
}

__attribute__((destructor))
static void shim_shutdown(void) {
    /* No flush here: the runtime's own teardown already destroyed the
     * context, and flushing now can deadlock. The intercepted
     * cudaDeviceSynchronize flushed everything the app waited for. */
    g_enabled = 0;
    if (g_out) { fflush(g_out); fclose(g_out); g_out = NULL; }
    debug("shutdown complete");
}

/* --- CUDA runtime API interposition ------------------------------------- */

typedef void (*cudaDeinitFn)(void);

cudaError_t cudaDeviceSynchronize(void);
cudaError_t cudaDeviceSynchronize_real(void);

static cudaError_t (*real_cudaDeviceSynchronize)(void) = NULL;

cudaError_t cudaDeviceSynchronize(void) {
    if (!real_cudaDeviceSynchronize) {
        real_cudaDeviceSynchronize = dlsym(RTLD_NEXT, "cudaDeviceSynchronize");
    }
    cudaError_t err = real_cudaDeviceSynchronize();
    if (g_enabled && g_out) {
        cuptiActivityFlushAll(0);
        fflush(g_out);
    }
    return err;
}

typedef int (*cudaEventSyncFn)(void *);

static void flush_if_enabled(void) {
    if (g_enabled && g_out) {
        cuptiActivityFlushAll(0);
        fflush(g_out);
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
