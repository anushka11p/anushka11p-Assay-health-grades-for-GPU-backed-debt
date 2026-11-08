# Assay — project plan

> 3 weeks · $0 · Go + CUDA + Python. The full design is in
> [`superpowers/specs/2026-11-08-assay-design.md`](superpowers/specs/2026-11-08-assay-design.md).
> Tick boxes as we go. **You** = only you can do it (your Colab/Kaggle accounts).
> **Us** = we build it together in this repo.

Start: Mon 9 Nov 2026 · Target finish: Sun 29 Nov 2026

## Milestones

| # | Date | Milestone | Done when |
|---|---|---|---|
| M1 | Fri 13 Nov | First real report | A signed `report.json` from a real Colab T4, readable on the Mac |
| M2 | Fri 20 Nov | Full health check | 5-min check (telemetry + all probes) works on Colab and Kaggle; `assay verify` passes |
| M3 | Thu 26 Nov | Census graded | ≥ 15 distinct physical GPUs collected and graded A–F |
| M4 | Sun 29 Nov | Shipped | Site live on GitHub Pages, README + methodology written, resume line filled in |

---

## Week 1 (9–13 Nov) — Agent + telemetry

**Day 1 · Mon — Setup**
- [x] **You:** install Go (`brew install go`) and make a GitHub repo `assay`
- [x] **Us:** Go module skeleton, `assay` CLI with `check` / `keygen` / `verify` stubs
- [x] **Us:** GitHub Actions CI running `go test` on every push

**Day 2 · Tue — Report schema + fake GPU**
- [x] **Us:** report schema v1 (identity, memory health, events, interconnect, thermals)
- [x] **Us:** fake backend + scenarios: `healthy`, `hbm-degrading`, `thermal-throttle`, `xid-storm`, `pcie-flaky`
- [x] **Us:** `assay check --backend=fake` writes a report on the Mac

**Day 3 · Wed — Signing**
- [x] **Us:** canonical JSON + golden-file tests
- [x] **Us:** Ed25519 `keygen`, sign, `verify`, with a tamper-detection test

**Day 4 · Thu — Real NVML backend**
- [x] **Us:** NVML backend via `go-nvml` (identity, ECC, retired pages, PCIe, thermals/power/clocks, throttle reasons, XID events)
- [x] **Us:** treat `NotSupported` as `null` + reason, not an error

**Day 5 · Fri — First Colab run → M1**
- [x] **Us:** `notebooks/colab_check.ipynb` bootstrap cell (install Go, clone, build, run, download)
- [x] **You:** run it on Colab, download `report.json`, drop it in `census/raw/` (8 Nov: Tesla T4 serial 1564520009069, clean)
- [x] **Us:** fix whatever real hardware breaks (nothing broke; every field read except `row_remap`, which T4s don't have)

> **Learned from the first real report:** an idle T4 drops its PCIe link to Gen1 to save
> power. Link speed only counts when measured **under load** (Week 2 probes); the grader
> must never flag an idle link.

## Week 2 (16–20 Nov) — Probes + transparency log

**Day 6 · Mon — First probes**
- [ ] **Us:** `probes/` Makefile; `gemm` (cuBLAS FP16/FP32 TFLOPS) and `membw` (copy/triad GB/s)
- [ ] **You:** quick Colab run to check that the numbers look sane for a T4

**Day 7 · Tue — Soak + memtest**
- [ ] **Us:** `soak` (3-min sustained load; records clock/temp/power over time; time until throttling)
- [ ] **Us:** `memtest` (pattern write/read-back, with a mismatch-injection self-test)

**Day 8 · Wed — Orchestration**
- [ ] **Us:** agent runs probes with timeouts, an idle-GPU check first, and 250 ms NVML sampling during probes
- [ ] **Us:** probe results + time series go into the report

**Day 9 · Thu — Transparency log**
- [ ] **Us:** RFC 6962 Merkle tree: `log append`, signed tree head, inclusion and consistency proofs
- [ ] **Us:** test against RFC 6962 vectors; `verify` checks the signature and inclusion

**Day 10 · Fri — Kaggle + multi-GPU → M2**
- [ ] **Us:** `notebooks/kaggle_check.ipynb`; `allreduce` probe (NCCL) for 2× T4
- [ ] **You:** run full checks on Colab + Kaggle; append all reports to the log

## Week 3 (23–29 Nov) — Census, grading, site

> **You, ongoing from Week 2:** run a check every time you open a Colab or Kaggle
> session (own accounts only). Goal: ≥ 15 distinct GPUs (by serial + UUID).

**Day 11 · Mon — Census dataset**
- [ ] **Us:** dedupe by serial/UUID, build `census/census.json`, summary statistics

**Day 12 · Tue — Grading**
- [ ] **Us:** Python `grading/`: cohorts, percentile scores, hard flags, A–F grade + one-line reason
- [ ] **Us:** pytest with synthetic cohorts

**Day 13 · Wed — Site part 1**
- [ ] **Us:** Next.js static export: census overview page (counts, grade distribution, charts)

**Day 14 · Thu — Site part 2 → M3**
- [ ] **Us:** per-GPU health-statement page + "verify this report" panel
- [ ] **Us:** methodology + threat-model page

**Day 15 · Fri–Sun — Ship → M4**
- [ ] **Us:** deploy to GitHub Pages, publish the signed tree head
- [ ] **Us:** README with demo GIF, architecture diagram, results
- [ ] **You:** fill in the resume line with the real N and X%, and optionally post the write-up

---

## Risks and fallbacks

| Risk | Fallback |
|---|---|
| Colab/Kaggle keep giving the same GPU | Spread sessions over different days/times; include college lab GPUs if available; report the honest N |
| A field isn't supported on T4/P100 | Record it as unsupported; that's a finding in itself |
| Kaggle 2× T4 not available on your account | Skip `allreduce`; leave it in the code, tested on fake data |
| Falling behind | Cut in this order: `allreduce` → consistency proofs → per-GPU charts. Never cut signing, the real NVML backend or grading |
| Free-tier rules change | Only your own accounts, no automation of session creation |

## After v1 (not now)

Daemon mode on lab servers · DCGM backend · nvtrust attestation on H100 CC · survival models once failure data exists
