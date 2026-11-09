// allreduce: how fast can GPUs in one machine combine their data?
//
// Multi-GPU training ends every step with an all-reduce: each GPU's gradients are
// summed and the result shared back to all of them. This probe times NCCL's
// all-reduce across every GPU in the machine and reports "bus bandwidth", the
// standard NCCL metric that is comparable across GPU counts.
//   busbw = data size / time * 2(n-1)/n
// Needs 2+ GPUs (e.g. Kaggle's "GPU T4 x2"); with one GPU it reports "skipped".
//
// Usage: allreduce [--seconds 10] [--mb 256]   (mb = buffer per GPU)
#include <string>
#include <vector>

#include <nccl.h>

#include "common.h"

#define NCCL_CHECK(probe, call)                                                         \
  do {                                                                                  \
    ncclResult_t r_ = (call);                                                           \
    if (r_ != ncclSuccess) fail(probe, std::string(#call) + " failed: " + ncclGetErrorString(r_)); \
  } while (0)

int main(int argc, char** argv) {
  const char* P = "allreduce";
  Args args = parse_args(P, argc, argv);

  int ndev = 0;
  CUDA_CHECK(P, cudaGetDeviceCount(&ndev));
  if (ndev < 2) {
    printf("{\"probe\":\"allreduce\",\"skipped\":\"needs 2 or more GPUs, found %d\"}\n", ndev);
    return 0;
  }

  long long mb = args.mb > 0 ? args.mb : 256;
  size_t count = (size_t)mb * 1024 * 1024 / sizeof(float);
  size_t bytes = count * sizeof(float);

  std::vector<int> devs(ndev);
  for (int i = 0; i < ndev; i++) devs[i] = i;
  std::vector<ncclComm_t> comms(ndev);
  NCCL_CHECK(P, ncclCommInitAll(comms.data(), ndev, devs.data()));

  std::vector<float*> buf(ndev);
  std::vector<cudaStream_t> streams(ndev);
  for (int i = 0; i < ndev; i++) {
    CUDA_CHECK(P, cudaSetDevice(i));
    CUDA_CHECK(P, cudaMalloc(&buf[i], bytes));
    CUDA_CHECK(P, cudaMemset(buf[i], 0, bytes));
    CUDA_CHECK(P, cudaStreamCreate(&streams[i]));
  }

  // One all-reduce across all GPUs, waiting until every GPU has finished.
  auto all_reduce = [&]() {
    NCCL_CHECK(P, ncclGroupStart());
    for (int i = 0; i < ndev; i++)
      NCCL_CHECK(P, ncclAllReduce(buf[i], buf[i], count, ncclFloat, ncclSum, comms[i], streams[i]));
    NCCL_CHECK(P, ncclGroupEnd());
    for (int i = 0; i < ndev; i++) {
      CUDA_CHECK(P, cudaSetDevice(i));
      CUDA_CHECK(P, cudaStreamSynchronize(streams[i]));
    }
  };

  for (int i = 0; i < 5; i++) all_reduce();  // warm-up: sets up NCCL's channels

  long long iters = 0;
  auto t0 = Clock::now();
  while (since(t0) < args.seconds) {
    all_reduce();
    iters++;
  }
  double secs = since(t0);

  double algbw = (double)bytes * iters / secs;           // bytes/s
  double busbw = algbw * 2.0 * (ndev - 1) / ndev;        // NCCL's comparable metric

  for (int i = 0; i < ndev; i++) {
    CUDA_CHECK(P, cudaSetDevice(i));
    cudaFree(buf[i]);
    cudaStreamDestroy(streams[i]);
    ncclCommDestroy(comms[i]);
  }
  printf("{\"probe\":\"allreduce\",\"gpus\":%d,\"mib_per_gpu\":%lld,\"iterations\":%lld,"
         "\"algbw_mb_per_s\":%lld,\"busbw_mb_per_s\":%lld}\n",
         ndev, mb, iters, (long long)(algbw / 1e6), (long long)(busbw / 1e6));
  return 0;
}
