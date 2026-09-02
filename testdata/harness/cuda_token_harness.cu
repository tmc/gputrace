#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <cuda_runtime.h>
#include <unistd.h>

__global__ void harness_token_step(int *d_out, int n, int step) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) {
        d_out[idx] += step;
    }
}

__global__ void harness_aux_compute(float *d_weights, float *d_act, int n) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < n) {
        d_act[idx] = d_act[idx] * d_weights[idx] + 1.0f;
    }
}

int main(int argc, char **argv) {
    int tokens = 128;
    int stage_weights = 1;
    int perturb_mid = 0;
    int drop_tail = 0;

    for (int i = 1; i < argc; ++i) {
        if (strcmp(argv[i], "--tokens") == 0 && i + 1 < argc) {
            tokens = atoi(argv[++i]);
        } else if (strcmp(argv[i], "--no-staging") == 0) {
            stage_weights = 0;
        } else if (strcmp(argv[i], "--perturb") == 0) {
            perturb_mid = 1;
        } else if (strcmp(argv[i], "--drop-tail") == 0) {
            drop_tail = 1;
        }
    }

    const int N = 1024 * 256;
    float *h_weights = (float*)malloc(N * sizeof(float));
    for (int i = 0; i < N; ++i) h_weights[i] = 1.0f;

    float *d_weights, *d_act;
    int *d_out;
    cudaMalloc(&d_weights, N * sizeof(float));
    cudaMalloc(&d_act, N * sizeof(float));
    cudaMalloc(&d_out, 1024 * sizeof(int));

    if (stage_weights) {
        cudaMemcpy(d_weights, h_weights, N * sizeof(float), cudaMemcpyHostToDevice);
    }

    // Prefill launch (token 0 / prefill)
    harness_aux_compute<<<32, 256>>>(d_weights, d_act, N);
    harness_token_step<<<4, 256>>>(d_out, 1024, 0);
    cudaDeviceSynchronize();

    // Decode loop
    for (int t = 1; t <= tokens; ++t) {
        if (drop_tail && t > tokens - 20) {
            _exit(0);
        }

        if (perturb_mid && t >= 48 && t <= 80) {
            usleep(25000);
        } else {
            usleep(5000);
        }

        harness_aux_compute<<<32, 256>>>(d_weights, d_act, N);
        harness_token_step<<<4, 256>>>(d_out, 1024, t);
        cudaDeviceSynchronize();
    }

    int h_out[1024];
    cudaMemcpy(h_out, d_out, 1024 * sizeof(int), cudaMemcpyDeviceToHost);
    cudaFree(d_weights);
    cudaFree(d_act);
    cudaFree(d_out);
    free(h_weights);

    printf("Harness completed: tokens=%d, staging=%d\n", tokens, stage_weights);
    return 0;
}
