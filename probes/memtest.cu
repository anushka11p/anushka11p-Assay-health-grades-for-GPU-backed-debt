// memtest: does every bit of GPU memory hold what was written to it?
//
// Writes known patterns across most of the free memory, reads them back and counts
// words that came back wrong. Six patterns catch different faults:
//   all zeros, all ones           stuck-at bits
//   0xAAAAAAAA, 0x55555555        neighbouring bits interfering with each other
//   address-in-address            addressing faults (two addresses hitting one cell)
//   pseudo-random                 data-dependent faults
//
// Note: on ECC GPUs the hardware silently fixes single-bit errors, so memtest
// mostly catches what ECC can't (multi-bit or addressing faults). The ECC
// counters in the report cover the rest.
//
// --inject plants exactly one wrong word to prove detection works: expect
// mismatches = 1 and "self_test":"pass".
//
// Usage: memtest [--device 0] [--mb 0 = 75% of free memory] [--inject]
#include <cstdint>
#include <string>

#include "common.h"

constexpr int kPatterns = 6;
constexpr int kMaxReported = 8;  // first N bad offsets kept for the report

__device__ __host__ inline uint32_t pattern(int p, size_t i) {
  switch (p) {
    case 0: return 0x00000000u;
    case 1: return 0xFFFFFFFFu;
    case 2: return 0xAAAAAAAAu;
    case 3: return 0x55555555u;
    case 4: return (uint32_t)i ^ (uint32_t)(i >> 32);
    default: {  // splitmix-style hash of the index
      uint64_t x = (uint64_t)i * 0x9E3779B97F4A7C15ull + (uint64_t)p;
      x ^= x >> 31;
      x *= 0xBF58476D1CE4E5B9ull;
      x ^= x >> 29;
      return (uint32_t)x;
    }
  }
}

__global__ void write_pattern(uint32_t* mem, size_t n, int p) {
  for (size_t i = blockIdx.x * (size_t)blockDim.x + threadIdx.x; i < n; i += (size_t)gridDim.x * blockDim.x)
    mem[i] = pattern(p, i);
}

__global__ void check_pattern(const uint32_t* mem, size_t n, int p, unsigned long long* bad,
                              unsigned long long* offsets, unsigned int* slots) {
  for (size_t i = blockIdx.x * (size_t)blockDim.x + threadIdx.x; i < n; i += (size_t)gridDim.x * blockDim.x) {
    if (mem[i] != pattern(p, i)) {
      atomicAdd(bad, 1ull);
      unsigned int slot = atomicAdd(slots, 1u);
      if (slot < kMaxReported) offsets[slot] = (unsigned long long)(i * sizeof(uint32_t));
    }
  }
}

int main(int argc, char** argv) {
  const char* P = "memtest";
  Args args = parse_args(P, argc, argv);
  select_device(P, args.device);

  size_t free_b = 0, total_b = 0;
  CUDA_CHECK(P, cudaMemGetInfo(&free_b, &total_b));
  size_t bytes = args.mb > 0 ? (size_t)args.mb * 1024 * 1024 : (free_b / 4 * 3);
  bytes = bytes / (1024 * 1024) * (1024 * 1024);  // whole MiB
  if (bytes == 0 || bytes > free_b) fail(P, "requested memory not available");
  size_t n = bytes / sizeof(uint32_t);

  uint32_t* mem;
  unsigned long long *bad, *offsets;
  unsigned int* slots;
  CUDA_CHECK(P, cudaMalloc(&mem, bytes));
  CUDA_CHECK(P, cudaMalloc(&bad, sizeof(unsigned long long)));
  CUDA_CHECK(P, cudaMalloc(&offsets, kMaxReported * sizeof(unsigned long long)));
  CUDA_CHECK(P, cudaMalloc(&slots, sizeof(unsigned int)));
  CUDA_CHECK(P, cudaMemset(bad, 0, sizeof(unsigned long long)));
  CUDA_CHECK(P, cudaMemset(offsets, 0, kMaxReported * sizeof(unsigned long long)));
  CUDA_CHECK(P, cudaMemset(slots, 0, sizeof(unsigned int)));

  int sms = 0;
  CUDA_CHECK(P, cudaDeviceGetAttribute(&sms, cudaDevAttrMultiProcessorCount, args.device));
  const int blocks = sms * 8, threads = 256;

  for (int p = 0; p < kPatterns; p++) {
    write_pattern<<<blocks, threads>>>(mem, n, p);
    CUDA_CHECK(P, cudaGetLastError());
    CUDA_CHECK(P, cudaDeviceSynchronize());
    if (args.inject && p == 0) {
      // Plant one wrong word in the middle, behind the GPU's back.
      uint32_t wrong = ~pattern(0, n / 2);
      CUDA_CHECK(P, cudaMemcpy(mem + n / 2, &wrong, sizeof(wrong), cudaMemcpyHostToDevice));
    }
    check_pattern<<<blocks, threads>>>(mem, n, p, bad, offsets, slots);
    CUDA_CHECK(P, cudaGetLastError());
    CUDA_CHECK(P, cudaDeviceSynchronize());
  }

  unsigned long long mismatches = 0, host_offsets[kMaxReported] = {0};
  unsigned int used = 0;
  CUDA_CHECK(P, cudaMemcpy(&mismatches, bad, sizeof(mismatches), cudaMemcpyDeviceToHost));
  CUDA_CHECK(P, cudaMemcpy(host_offsets, offsets, sizeof(host_offsets), cudaMemcpyDeviceToHost));
  CUDA_CHECK(P, cudaMemcpy(&used, slots, sizeof(used), cudaMemcpyDeviceToHost));
  cudaFree(mem);
  cudaFree(bad);
  cudaFree(offsets);
  cudaFree(slots);

  std::string list;
  for (unsigned int i = 0; i < used && i < (unsigned)kMaxReported; i++)
    list += (i ? "," : "") + std::to_string(host_offsets[i]);

  std::string self_test;
  if (args.inject) self_test = std::string(",\"self_test\":\"") + (mismatches == 1 ? "pass" : "fail") + "\"";

  printf("{\"probe\":\"memtest\",\"device\":%d,\"mib_tested\":%lld,\"patterns\":%d,"
         "\"mismatches\":%llu,\"first_bad_offsets\":[%s]%s}\n",
         args.device, (long long)(bytes / (1024 * 1024)), kPatterns, mismatches, list.c_str(), self_test.c_str());
  return 0;
}
