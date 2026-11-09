// Shared helpers for the Assay CUDA probes.
//
// Every probe is a small program that stress-tests one part of a GPU and prints
// exactly ONE line of JSON to stdout. The agent runs the probe, parses that line
// and puts it in the signed report. Rules every probe follows:
//
//   - Integers only (GFLOPS, MB/s, counts). Reports are signed byte-for-byte and
//     integers have exactly one JSON spelling.
//   - On failure, print {"probe":"<name>","error":"..."} and exit 1.
//   - If the probe can't apply here, print {"probe":"<name>","skipped":"<why>"}.
//   - Read-only towards GPU settings: probes only allocate memory and run kernels.
#pragma once

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

#include <cuda_runtime.h>

// escape makes a string safe to embed in JSON.
inline std::string escape(const std::string& s) {
  std::string out;
  for (char c : s) {
    if (c == '"' || c == '\\') {
      out += '\\';
      out += c;
    } else if ((unsigned char)c < 0x20) {
      out += ' ';
    } else {
      out += c;
    }
  }
  return out;
}

[[noreturn]] inline void fail(const char* probe, const std::string& msg) {
  printf("{\"probe\":\"%s\",\"error\":\"%s\"}\n", probe, escape(msg).c_str());
  fflush(stdout);
  exit(1);
}

#define CUDA_CHECK(probe, call)                                                         \
  do {                                                                                  \
    cudaError_t err_ = (call);                                                          \
    if (err_ != cudaSuccess)                                                            \
      fail(probe, std::string(#call) + " failed: " + cudaGetErrorString(err_));         \
  } while (0)

// Args are the command-line flags shared by all probes.
struct Args {
  int device = 0;        // --device N    which GPU
  double seconds = 10;   // --seconds S   how long to measure
  long long mb = 0;      // --mb M        memory to use (0 = probe default)
  int n = 4096;          // --n N         matrix size for GEMM probes
  double window = 10;    // --window S    soak: seconds per throughput window
  bool inject = false;   // --inject      memtest: plant one error to prove detection works
};

inline Args parse_args(const char* probe, int argc, char** argv) {
  Args a;
  for (int i = 1; i < argc; i++) {
    std::string f = argv[i];
    auto next = [&]() -> const char* {
      if (i + 1 >= argc) fail(probe, "missing value for " + f);
      return argv[++i];
    };
    if (f == "--device") a.device = atoi(next());
    else if (f == "--seconds") a.seconds = atof(next());
    else if (f == "--mb") a.mb = atoll(next());
    else if (f == "--n") a.n = atoi(next());
    else if (f == "--window") a.window = atof(next());
    else if (f == "--inject") a.inject = true;
    else fail(probe, "unknown flag " + f);
  }
  if (a.seconds <= 0 || a.n <= 0 || a.window <= 0 || a.mb < 0) fail(probe, "flags must be positive");
  return a;
}

// select_device picks the GPU and checks it exists.
inline void select_device(const char* probe, int device) {
  int count = 0;
  CUDA_CHECK(probe, cudaGetDeviceCount(&count));
  if (device < 0 || device >= count)
    fail(probe, "device " + std::to_string(device) + " not found (" + std::to_string(count) + " GPUs)");
  CUDA_CHECK(probe, cudaSetDevice(device));
}

// Wall-clock seconds since t0.
using Clock = std::chrono::steady_clock;
inline double since(Clock::time_point t0) {
  return std::chrono::duration<double>(Clock::now() - t0).count();
}

// GPU-side timer around a batch of work.
struct GpuTimer {
  const char* probe;
  cudaEvent_t start{}, stop{};
  explicit GpuTimer(const char* p) : probe(p) {
    CUDA_CHECK(probe, cudaEventCreate(&start));
    CUDA_CHECK(probe, cudaEventCreate(&stop));
  }
  ~GpuTimer() {
    cudaEventDestroy(start);
    cudaEventDestroy(stop);
  }
  void begin() { CUDA_CHECK(probe, cudaEventRecord(start)); }
  // end waits for the batch to finish and returns its time in milliseconds.
  float end() {
    CUDA_CHECK(probe, cudaEventRecord(stop));
    CUDA_CHECK(probe, cudaEventSynchronize(stop));
    CUDA_CHECK(probe, cudaGetLastError());
    float ms = 0;
    CUDA_CHECK(probe, cudaEventElapsedTime(&ms, start, stop));
    return ms;
  }
};
