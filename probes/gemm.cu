// gemm: how fast can this GPU multiply matrices?
//
// Matrix multiplication is most of the work in AI training and inference, so this
// is the headline number for "is this GPU as fast as it should be?". Measures:
//   fp32_gflops  plain 32-bit float (CUDA cores)
//   fp16_gflops  16-bit inputs with 32-bit accumulation (tensor cores)
// A healthy GPU lands close to others of the same model; a weak one falls short.
//
// Usage: gemm [--device 0] [--seconds 10] [--n 4096]   (seconds is per precision)
#include "gemm.h"

int main(int argc, char** argv) {
  const char* P = "gemm";
  Args args = parse_args(P, argc, argv);
  select_device(P, args.device);

  long long fp32_calls = 0, fp16_calls = 0, fp32 = 0, fp16 = 0;
  {
    Gemm<float> g(P, args.n);
    fp32 = gflops_for(g, args.seconds, &fp32_calls);
  }
  {
    Gemm<__half> g(P, args.n);
    fp16 = gflops_for(g, args.seconds, &fp16_calls);
  }

  printf("{\"probe\":\"gemm\",\"device\":%d,\"n\":%d,\"seconds_each\":%lld,"
         "\"fp32_gflops\":%lld,\"fp32_calls\":%lld,\"fp16_gflops\":%lld,\"fp16_calls\":%lld}\n",
         args.device, args.n, (long long)args.seconds, fp32, fp32_calls, fp16, fp16_calls);
  return 0;
}
