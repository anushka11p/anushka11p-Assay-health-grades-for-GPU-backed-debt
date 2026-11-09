// soak: does this GPU hold its speed under sustained load?
//
// A short benchmark can look fine on a GPU with dried-out thermal paste or a weak
// fan: it only slows down once it has heated up. soak runs FP16 matrix multiplies
// non-stop and records throughput in fixed windows. A healthy GPU stays flat; a
// struggling one sags as it heats up. (The agent samples temperature, clocks and
// throttle reasons alongside, so the report shows WHY it sagged.)
//
// Output: gflops per window, first/last/min window, and drop_pct = how far the
// last window fell below the first (negative = it got faster, e.g. warm boost).
//
// Usage: soak [--device 0] [--seconds 180] [--window 10] [--n 4096]
#include <algorithm>
#include <string>
#include <vector>

#include "gemm.h"

int main(int argc, char** argv) {
  const char* P = "soak";
  Args args = parse_args(P, argc, argv);
  select_device(P, args.device);
  if (args.window > args.seconds) fail(P, "--window must not exceed --seconds");

  Gemm<__half> g(P, args.n);
  for (int i = 0; i < 3; i++) g.run();
  CUDA_CHECK(P, cudaDeviceSynchronize());

  GpuTimer timer(P);
  std::vector<long long> windows;
  auto start = Clock::now();
  while (since(start) + 1e-9 < args.seconds) {
    double win_ms = 0;
    long long calls = 0;
    auto w0 = Clock::now();
    while (since(w0) < args.window && since(start) < args.seconds) {
      timer.begin();
      for (int i = 0; i < 10; i++) g.run();
      win_ms += timer.end();
      calls += 10;
    }
    if (calls == 0) break;  // time ran out between checks: no partial empty window
    windows.push_back((long long)(g.flops_per_call() * calls / (win_ms / 1000.0) / 1e9));
  }

  long long first = windows.front(), last = windows.back();
  long long lowest = *std::min_element(windows.begin(), windows.end());
  long long drop_pct = first > 0 ? (first - last) * 100 / first : 0;

  std::string list;
  for (size_t i = 0; i < windows.size(); i++) list += (i ? "," : "") + std::to_string(windows[i]);
  printf("{\"probe\":\"soak\",\"device\":%d,\"seconds\":%lld,\"window_seconds\":%lld,"
         "\"window_gflops\":[%s],\"first_gflops\":%lld,\"last_gflops\":%lld,\"min_gflops\":%lld,"
         "\"drop_pct\":%lld}\n",
         args.device, (long long)args.seconds, (long long)args.window, list.c_str(), first, last, lowest, drop_pct);
  return 0;
}
