// Matrix-multiply (GEMM) helpers shared by the gemm and soak probes.
#pragma once

#include <cublas_v2.h>
#include <cuda_fp16.h>

#include "common.h"

#define CUBLAS_CHECK(probe, call)                                                       \
  do {                                                                                  \
    cublasStatus_t st_ = (call);                                                        \
    if (st_ != CUBLAS_STATUS_SUCCESS)                                                   \
      fail(probe, std::string(#call) + " failed: cuBLAS status " + std::to_string((int)st_)); \
  } while (0)

// Fill with pseudo-random values in [-0.5, 0.5). Real-looking data matters:
// all-zero matrices draw less power and would make the GPU look cooler than it is.
template <typename T>
__global__ void fill_random(T* x, size_t n, unsigned seed) {
  for (size_t i = blockIdx.x * (size_t)blockDim.x + threadIdx.x; i < n; i += (size_t)gridDim.x * blockDim.x) {
    unsigned h = (unsigned)i * 2654435761u ^ seed;
    h ^= h >> 15;
    h *= 2246822519u;
    h ^= h >> 13;
    x[i] = (T)((float)(h & 0xffff) / 65536.0f - 0.5f);
  }
}

// Gemm owns three n x n matrices of one type and runs C = A * B with cuBLAS.
// Each call does 2 * n^3 floating-point operations.
template <typename T>
struct Gemm {
  const char* probe;
  int n;
  T *a = nullptr, *b = nullptr, *c = nullptr;
  cublasHandle_t handle{};

  Gemm(const char* p, int n_) : probe(p), n(n_) {
    size_t elems = (size_t)n * n;
    CUDA_CHECK(probe, cudaMalloc(&a, elems * sizeof(T)));
    CUDA_CHECK(probe, cudaMalloc(&b, elems * sizeof(T)));
    CUDA_CHECK(probe, cudaMalloc(&c, elems * sizeof(T)));
    fill_random<T><<<1024, 256>>>(a, elems, 1);
    fill_random<T><<<1024, 256>>>(b, elems, 2);
    CUDA_CHECK(probe, cudaGetLastError());
    CUDA_CHECK(probe, cudaDeviceSynchronize());
    CUBLAS_CHECK(probe, cublasCreate(&handle));
  }
  ~Gemm() {
    cublasDestroy(handle);
    cudaFree(a);
    cudaFree(b);
    cudaFree(c);
  }

  double flops_per_call() const { return 2.0 * n * (double)n * n; }

  void run();  // specialised below for float and __half
};

template <>
inline void Gemm<float>::run() {
  const float alpha = 1.0f, beta = 0.0f;
  CUBLAS_CHECK(probe, cublasSgemm(handle, CUBLAS_OP_N, CUBLAS_OP_N, n, n, n, &alpha, a, n, b, n, &beta, c, n));
}

// FP16 inputs/outputs with FP32 accumulation: the tensor-core path used for AI training.
template <>
inline void Gemm<__half>::run() {
  const float alpha = 1.0f, beta = 0.0f;
  CUBLAS_CHECK(probe, cublasGemmEx(handle, CUBLAS_OP_N, CUBLAS_OP_N, n, n, n, &alpha, a, CUDA_R_16F, n, b,
                                   CUDA_R_16F, n, &beta, c, CUDA_R_16F, n, CUBLAS_COMPUTE_32F,
                                   CUBLAS_GEMM_DEFAULT));
}

// gflops_for runs g back-to-back for about `seconds` and returns achieved GFLOPS.
// Work is timed on the GPU in batches of 10 calls; *calls receives the call count.
template <typename T>
long long gflops_for(Gemm<T>& g, double seconds, long long* calls) {
  for (int i = 0; i < 3; i++) g.run();  // warm-up: first calls include setup costs
  CUDA_CHECK(g.probe, cudaDeviceSynchronize());

  GpuTimer timer(g.probe);
  double total_ms = 0;
  long long done = 0;
  auto t0 = Clock::now();
  while (since(t0) < seconds) {
    timer.begin();
    for (int i = 0; i < 10; i++) g.run();
    total_ms += timer.end();
    done += 10;
  }
  if (calls) *calls = done;
  return (long long)(g.flops_per_call() * done / (total_ms / 1000.0) / 1e9);
}
