# Assay — design (DRAFT, not yet approved)

> Status: draft for review. Edit anything here directly; nothing is built yet.
> Date: 2026-11-08

## 1. What this version is

Assay v1 is a **portfolio project**: a real GPU health-check tool plus a small public
census of free cloud GPUs. It shows GPU-infra skills (NVML, CUDA microbenchmarks,
signed evidence, transparency logs, fleet-style grading) to infra teams at NVIDIA,
Google, Meta, Microsoft and AWS.

Constraints:

- **Budget: $0.** Development happens on a Mac with a fake GPU backend. Real hardware
  runs happen on free Google Colab (T4) and Kaggle (T4, P100, sometimes 2× T4) notebooks,
  using only your own accounts.
- **Time: about 3 weeks.**
- **Agent language: Go.**

Success means:

1. `assay check` runs on a real Colab or Kaggle GPU in about 5 minutes and writes a
   signed report.
2. `assay verify` proves that a report is unmodified and included in the transparency log.
3. A census of N distinct physical GPUs, each graded against its peers, published as a
   static site with a short methodology write-up.

Resume line: *"Built a GPU health-check tool in Go/CUDA. Tested N distinct T4 and P100
GPUs across Colab and Kaggle and found X% performing below their peers."*

## 2. Out of scope for v1

These are in the pitch but are left out on purpose, with the reason:

| Left out | Why |
|---|---|
| Kubernetes DaemonSet, long-running daemon | Notebooks can't run background services. Keep the code structure ready for it. |
| DCGM | Not installed on Colab/Kaggle. NVML covers what v1 needs. |
| OpenTelemetry → ClickHouse | Too much infrastructure for about 100 reports. JSON files are enough. |
| Survival / remaining-useful-life models | No labelled failure data. Grade *verified current condition* instead; the pitch's own risk section recommends this. |
| NVIDIA attestation (nvtrust) | Needs H100/Blackwell in confidential-compute mode. Documented as future work in the threat model. |
| Lender accounts, auth, billing | No real customers in v1. |

## 3. Architecture

```
 Colab / Kaggle notebook (Linux + real GPU)          Your Mac
 ┌───────────────────────────────────────┐   ┌──────────────────────────────┐
 │ bootstrap cell: clone repo, build     │   │ assay check --backend=fake   │
 │                                       │   │ (all dev + tests run here)   │
 │ assay check                           │   └──────────────────────────────┘
 │  ├─ telemetry  (NVML via go-nvml)     │
 │  ├─ probes     (CUDA binaries, JSON)  │   ┌──────────────────────────────┐
 │  ├─ report     (canonical JSON)       │──▶│ assay log append  report.json│
 │  └─ sign       (Ed25519)              │   │ assay verify      report.json│
 │        → report.json (download)       │   │ (Merkle transparency log)    │
 └───────────────────────────────────────┘   └──────────────┬───────────────┘
                                                            ▼
                                             grading/ (Python): peer grades
                                                            ▼
                                             site/: census + per-GPU pages
                                             (static, GitHub Pages)
```

### 3.1 Agent (`agent/`, Go)

A single binary, `assay`, with these subcommands:

- `assay check [--backend=nvml|fake] [--scenario=...] [--out report.json]`: collects
  telemetry, runs probes while sampling NVML in parallel, then writes and signs a report.
- `assay keygen`: creates the Ed25519 key for this install.
- `assay log append|prove|head`: the transparency log (see 3.3).
- `assay verify report.json`: checks the signature and the log inclusion proof.

**Telemetry backend interface.** `nvml` uses NVIDIA's `go-nvml` and loads
`libnvidia-ml.so` at runtime. `fake` produces realistic data from named scenarios
(`healthy`, `hbm-degrading`, `thermal-throttle`, `xid-storm`, `pcie-flaky`) so all
logic can be written and tested on the Mac.

Collected fields:

- **Identity:** UUID, board serial, PCI bus ID, model name, VBIOS version, driver and
  CUDA version, and a host fingerprint (provider and region, if detectable).
  Serial + UUID is how the census decides whether a GPU is new or one it has seen.
- **Memory health:** ECC mode; volatile and aggregate single-bit and double-bit error
  counts; retired pages (T4/P100) or row remapping (A100+). Whichever the GPU doesn't
  support is recorded as unsupported.
- **Events:** XID errors captured through an NVML event set during the run.
- **Interconnect:** PCIe link generation and width (current vs max), PCIe replay counter,
  NVLink errors when present.
- **Thermal and power:** temperature, power draw vs limit, SM and memory clocks, and
  throttle reasons, sampled every 250 ms during probes.

**Rules:**

- **Read-only.** Never change clocks, power limits, ECC mode or any other GPU setting.
- **Only on an idle GPU.** Before probing, check utilisation and running processes. If
  the GPU is busy, skip probes and record why.
- **Unsupported is not an error.** When NVML returns `NotSupported`, store `null` plus the
  reason. Only a missing driver or GPU makes the check fail.

### 3.2 Probes (`probes/`, CUDA C++)

Small standalone binaries, built with `nvcc` inside the notebook. Each one prints a single
JSON result. The agent runs them in order, with a per-probe timeout, while it samples
telemetry.

| Probe | Measures | Time |
|---|---|---|
| `gemm` | cuBLAS FP16 and FP32 matrix-multiply TFLOPS | ~60 s |
| `membw` | Device-memory bandwidth (copy and triad kernels), GB/s | ~30 s |
| `soak` | Sustained GEMM load; curve of clocks, temperature and power; time until throttling | ~3 min |
| `allreduce` | NCCL all-reduce bus bandwidth (runs only with 2+ GPUs, e.g. Kaggle 2× T4) | ~30 s |
| `memtest` | Writes and reads back known patterns across free memory; counts mismatches | ~30 s |

Total time is about 5 minutes. Results are compared with the card's spec sheet and with
other cards of the same model.

### 3.3 Report, signing and transparency log

- **Report:** versioned JSON schema (`schema_version: 1`), canonicalised (sorted keys,
  fixed number formatting) before hashing.
- **Signature:** Ed25519 over the SHA-256 of the canonical bytes. The agent's public key
  is embedded in the report.
- **Transparency log:** an append-only Merkle tree in the style of RFC 6962 (the design
  Certificate Transparency and Sigstore use), stored as files in the repo. It provides
  signed tree heads, inclusion proofs and consistency proofs. Publishing the tree head on
  the site means nobody, including you, can quietly edit or delete a report afterwards.

**Threat model (written plainly in the docs):** on T4/P100 the signature proves *this
agent key produced this report unmodified*. It does not prove that the GPU is the physical
device it claims to be. Doing that requires hardware attestation (nvtrust on H100 in
confidential-compute mode), which is the documented path for v2.

### 3.4 Grading (`grading/`, Python)

- **Cohorts:** GPUs are grouped by model and driver major version.
- **Peer score:** for each probe metric, the GPU's percentile within its cohort.
- **Hard flags,** which override the score: any double-bit ECC error, retired or pending
  pages, critical XIDs (for example 48, 63, 64, 79, 94, 95), a PCIe link running below max
  width, or thermal throttling below spec temperature.
- **Grade A–F** with a one-line reason for each grade, e.g. "C: membw 18% below T4 median;
  2 retired pages".
- Census output: `census/census.json` and summary statistics.

### 3.5 Site (`site/`)

A static Next.js export hosted free on GitHub Pages:

- **Census overview:** GPU count by provider and model, grade distribution, share of GPUs
  below their peers, and charts.
- **Per-GPU health statement:** a lender-style page with the grade, its reasons, probe
  results against the cohort, telemetry charts and a "verify this report" panel that shows
  the signature and inclusion proof.
- **Methodology and threat model** page.

## 4. Repo layout

```
assay/
  agent/        Go module: cmd/assay, internal/{telemetry,probe,report,sign,tlog}
  probes/       CUDA sources + Makefile
  notebooks/    colab_check.ipynb, kaggle_check.ipynb (bootstrap + run + download)
  log/          transparency log files
  census/       collected reports + census.json
  grading/      Python package + tests
  site/         Next.js static site
  docs/         specs, methodology, threat model
```

## 5. Three-week plan

**Week 1 — Agent + telemetry.**
- Go skeleton, report schema, fake backend with scenarios, canonical JSON, Ed25519 signing.
- NVML backend.
- Colab bootstrap notebook.
- Milestone: first signed report from a real T4.

**Week 2 — Probes + log.**
- CUDA probes and probe orchestration with parallel sampling.
- Transparency log and `assay verify`.
- Kaggle notebook, including the 2× T4 all-reduce probe if your account gets 2× T4.
- Milestone: full 5-minute check on Colab and Kaggle, with reports verified on the Mac.

**Week 3 — Census + grading + site.**
- Run a check every time you open a Colab or Kaggle session, and keep collecting
  throughout weeks 2–3.
- Grading package, static site and methodology write-up.
- Milestone: site published with N distinct GPUs.

## 6. Testing

- **Go:** unit tests against the fake backend for every scenario; golden-file tests for
  canonical JSON; signature round-trip tests, including tamper detection; Merkle tree
  tests against RFC 6962 test vectors.
- **Probes:** sanity bounds (a T4's FP16 TFLOPS must fall within a plausible range of its
  spec); `memtest` self-check using a deliberately injected mismatch.
- **Grading:** pytest with synthetic cohorts, covering hard-flag precedence and edge cases
  such as a cohort with only one GPU.
- **End-to-end:** `assay check --backend=fake` → `log append` → `verify` → grading runs
  in CI (GitHub Actions, free) on every push.

## 7. Open questions for you

1. Is the scope above right? Is anything missing, or anything you'd cut?
2. Do you want the census site public from day one, or private until it's done?
3. Do you have access to college or lab GPU servers? They would be a strong test for a
   later daemon mode.
