// membw: how fast can this GPU move data through its memory?
//
// Many AI workloads are limited by memory bandwidth, not compute. Two classic
// STREAM-style kernels, each reading/writing big arrays far larger than any cache:
//   copy   b[i] = a[i]             (2 arrays touched per element)
//   triad  a[i] = b[i] + s * c[i]  (3 arrays touched per element)
// Reported in MB/s (1 MB = 1,000,000 bytes, the unit spec sheets use).
//
// Usage: membw [--device 0] [--seconds 10] [--mb 1536]   (mb = total for 3 arrays)
#include "common.h"

// float4 = 16-byte loads/stores, which lets the memory system run near full speed.
__global__ void copy_kernel(const float4* __restrict__ a, float4* __restrict__ b, size_t n) {
  for (size_t i = blockIdx.x * (size_t)blockDim.x + threadIdx.x; i < n; i += (size_t)gridDim.x * blockDim.x)
    b[i] = a[i];
}

__global__ void triad_kernel(float4* __restrict__ a, const float4* __restrict__ b, const float4* __restrict__ c,
                             float s, size_t n) {
  for (size_t i = blockIdx.x * (size_t)blockDim.x + threadIdx.x; i < n; i += (size_t)gridDim.x * blockDim.x) {
    float4 x = b[i], y = c[i];
    a[i] = make_float4(x.x + s * y.x, x.y + s * y.y, x.z + s * y.z, x.w + s * y.w);
  }
}

__global__ void fill(float4* x, size_t n, float v) {
  for (size_t i = blockIdx.x * (size_t)blockDim.x + threadIdx.x; i < n; i += (size_t)gridDim.x * blockDim.x)
    x[i] = make_float4(v, v, v, v);
}

int main(int argc, char** argv) {
  const char* P = "membw";
  Args args = parse_args(P, argc, argv);
  select_device(P, args.device);
  long long total_mb = args.mb > 0 ? args.mb : 1536;

  size_t bytes_each = (size_t)(total_mb / 3) * 1024 * 1024;
  size_t n = bytes_each / sizeof(float4);
  if (n == 0) fail(P, "--mb too small");

  float4 *a, *b, *c;
  CUDA_CHECK(P, cudaMalloc(&a, n * sizeof(float4)));
  CUDA_CHECK(P, cudaMalloc(&b, n * sizeof(float4)));
  CUDA_CHECK(P, cudaMalloc(&c, n * sizeof(float4)));

  int sms = 0;
  CUDA_CHECK(P, cudaDeviceGetAttribute(&sms, cudaDevAttrMultiProcessorCount, args.device));
  const int blocks = sms * 8, threads = 256;
  fill<<<blocks, threads>>>(a, n, 1.0f);
  fill<<<blocks, threads>>>(b, n, 2.0f);
  fill<<<blocks, threads>>>(c, n, 0.5f);
  CUDA_CHECK(P, cudaDeviceSynchronize());

  GpuTimer timer(P);
  // Runs one kernel repeatedly for half the time budget; returns MB/s.
  auto measure = [&](auto launch, double arrays_touched) -> long long {
    launch();  // warm-up
    CUDA_CHECK(P, cudaDeviceSynchronize());
    double total_ms = 0;
    long long reps = 0;
    auto t0 = Clock::now();
    while (since(t0) < args.seconds / 2) {
      timer.begin();
      for (int i = 0; i < 10; i++) launch();
      total_ms += timer.end();
      reps += 10;
    }
    double bytes = arrays_touched * (double)n * sizeof(float4) * reps;
    return (long long)(bytes / (total_ms / 1000.0) / 1e6);
  };

  long long copy = measure([&] { copy_kernel<<<blocks, threads>>>(a, b, n); }, 2);
  long long triad = measure([&] { triad_kernel<<<blocks, threads>>>(a, b, c, 3.0f, n); }, 3);

  cudaFree(a);
  cudaFree(b);
  cudaFree(c);
  printf("{\"probe\":\"membw\",\"device\":%d,\"mib_per_array\":%lld,"
         "\"copy_mb_per_s\":%lld,\"triad_mb_per_s\":%lld}\n",
         args.device, (long long)(bytes_each / (1024 * 1024)), copy, triad);
  return 0;
}
