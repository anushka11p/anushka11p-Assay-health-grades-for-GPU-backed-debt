"""Generates colab_check.ipynb and kaggle_check.ipynb from one source.

Both notebooks do the same thing (build Assay and its CUDA probes, run a full
health check, verify it, package the report); only paths and the download step
differ. Edit this file, then run:

    python3 notebooks/make_notebooks.py
"""

import json
import pathlib

HERE = pathlib.Path(__file__).parent
REPO = "https://github.com/anushka11p/anushka11p-Assay-health-grades-for-GPU-backed-debt.git"


def md(text):
    return {"cell_type": "markdown", "metadata": {}, "source": text.strip("\n").splitlines(keepends=True)}


def code(text):
    return {"cell_type": "code", "metadata": {}, "execution_count": None, "outputs": [],
            "source": text.strip("\n").splitlines(keepends=True)}


def cells(p):
    """p holds the provider-specific bits."""
    return [
        md(f"""
# Assay: GPU health check on {p['title']}

Builds Assay and its CUDA stress tests on the free GPU {p['title']} lends you, runs a full
health check (about 5 minutes), verifies the signed report and packages it for download.

**Before you start:** {p['setup']}

Read-only: Assay never changes any GPU setting. It only allocates memory and runs its own tests.
"""),
        md("""
## 1. Which GPU(s) did we get?
Each new session may give you a different physical GPU. The serial number tells them apart.
"""),
        code("""
!nvidia-smi --query-gpu=index,name,serial,uuid,compute_cap,driver_version,temperature.gpu --format=csv
"""),
        md("""
## 2. Settings
`BRANCH`: the repo branch to build (`main` once your work is merged).
`SOAK`: how long the sustained-load test runs. 3 minutes is the standard; use `60s` for a quick try.
"""),
        code(f"""
REPO = "{REPO}"
BRANCH = "main"
GO_VERSION = "1.27.3"   # must be >= the version in agent/go.mod
SOAK = "180s"
WORK = "{p['work']}"     # where the report is saved
SRC = "/tmp/assay-src"
"""),
        md("""
## 3. Install Go (skipped if already installed this session)
"""),
        code("""
%%bash -s "$GO_VERSION"
set -euo pipefail
if ! /tmp/go-sdk/go/bin/go version 2>/dev/null | grep -q "go$1 "; then
  rm -rf /tmp/go-sdk && mkdir -p /tmp/go-sdk
  curl -fsSL "https://go.dev/dl/go$1.linux-amd64.tar.gz" | tar -C /tmp/go-sdk -xz
fi
/tmp/go-sdk/go/bin/go version
"""),
        md("""
## 4. Download Assay and build the agent
"""),
        code("""
%%bash -s "$REPO" "$BRANCH" "$SRC"
set -euo pipefail
rm -rf "$3"
git clone --quiet --depth 1 --branch "$2" "$1" "$3"
cd "$3/agent"
# NVIDIA's C headers print many harmless "deprecated" warnings; show only real errors.
if ! /tmp/go-sdk/go/bin/go build -o /tmp/assay ./cmd/assay 2>/tmp/build.log; then
  grep -v -e deprecated -e nvml.h -e cgo-gcc-prolog /tmp/build.log
  exit 1
fi
/tmp/assay version
"""),
        md("""
## 5. Build the CUDA stress tests
Compiled for exactly this GPU's generation (e.g. `sm_75` for a T4, `sm_60` for a P100).
With 2+ GPUs it also builds the all-reduce test, fetching NVIDIA's NCCL library with pip if needed.
"""),
        code("""
%%bash -s "$SRC"
set -euo pipefail
export PATH=/usr/local/cuda/bin:$PATH
ARCH="sm_$(nvidia-smi --query-gpu=compute_cap --format=csv,noheader | head -1 | tr -d .)"
echo "building probes for $ARCH"
make -s -C "$1/probes" ARCH="$ARCH"
GPUS=$(nvidia-smi -L | wc -l)
if [ "$GPUS" -ge 2 ]; then
  if ! make -s -C "$1/probes" allreduce ARCH="$ARCH" 2>/dev/null; then
    echo "system NCCL not found; installing it with pip"
    pip -q install nvidia-nccl-cu12
    NCCL_HOME=$(python3 -c "import nvidia.nccl as n; print(list(n.__path__)[0])")
    make -s -C "$1/probes" allreduce ARCH="$ARCH" NCCL_HOME="$NCCL_HOME" NCCL_LIB="-l:libnccl.so.2"
  fi
else
  echo "1 GPU: skipping the all-reduce test (needs 2+)"
fi
ls "$1/probes/bin"
"""),
        md("""
## 6. Prove memtest can actually catch a bad bit
Plants one wrong word in GPU memory and checks memtest finds it. Expect `"mismatches":1` and `"self_test":"pass"`.
"""),
        code("""
!{SRC}/probes/bin/memtest --mb 256 --inject
"""),
        md("""
## 7. Create a signing key for this session
The session is wiped when it ends, so each session gets a fresh key. Its public key is packaged with the report.
"""),
        code("""
!test -f /tmp/assay-keys/agent.key || /tmp/assay keygen --dir /tmp/assay-keys
"""),
        md("""
## 8. Run the full health check (about 5 minutes)
1. 5 seconds of idle readings
2. Stress tests, in order: matrix multiply → memory bandwidth → memory pattern test → soak (+ all-reduce with 2+ GPUs)
3. Temperature, power, clocks and PCIe link sampled every 250 ms throughout
"""),
        code(f"""
import datetime, os
os.makedirs(WORK, exist_ok=True)
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
REPORT = f"{{WORK}}/report-{p['name']}-{{stamp}}.json"
!/tmp/assay check --backend=nvml --key /tmp/assay-keys/agent.key --probe-dir {{SRC}}/probes/bin --soak {{SOAK}} --out {{REPORT}}
"""),
        md("""
## 9. Verify the signature
Pinned to this session's public key: checks that the report is unmodified *and* who signed it.
"""),
        code("""
!/tmp/assay verify --pubkey /tmp/assay-keys/agent.pub {REPORT}
"""),
        md(f"""
## 10. Package the report with its public key
{p['download_text']}
Unzip into `census/raw/` in your repo, keeping the `.json` and `.pub` together.
"""),
        code(f"""
import shutil, tempfile
pkg = tempfile.mkdtemp()
shutil.copy(REPORT, pkg)
shutil.copy("/tmp/assay-keys/agent.pub", REPORT.replace(".json", ".pub").replace(WORK, pkg))
ZIP = shutil.make_archive(REPORT[:-5], "zip", pkg)
print("packaged", ZIP)
{p['download_code']}
"""),
    ]


PROVIDERS = [
    dict(
        file="colab_check.ipynb", name="colab", title="Google Colab", work="/content",
        setup="Runtime → Change runtime type → **T4 GPU** → Save. Then Runtime → Run all.",
        download_text="The zip downloads to your computer automatically.",
        download_code="from google.colab import files\nfiles.download(ZIP)",
        metadata={"accelerator": "GPU", "colab": {"provenance": [], "gpuType": "T4"}},
    ),
    dict(
        file="kaggle_check.ipynb", name="kaggle", title="Kaggle", work="/kaggle/working",
        setup=("in the notebook's right-hand panel, Session options → Accelerator → **GPU T4 x2** "
               "(two GPUs, so the all-reduce test runs too) or **GPU P100**, and Internet → **On** "
               "(needs a phone-verified Kaggle account). Then Run all."),
        download_text="The zip is saved in `/kaggle/working`: open the **Output** panel on the right and download it.",
        download_code='print("download it from the Output panel →")',
        metadata={},
    ),
]

for p in PROVIDERS:
    nb = {
        "cells": cells(p),
        "metadata": {**p["metadata"], "kernelspec": {"display_name": "Python 3", "name": "python3"},
                     "language_info": {"name": "python"}},
        "nbformat": 4,
        "nbformat_minor": 0,
    }
    (HERE / p["file"]).write_text(json.dumps(nb, indent=1) + "\n")
    print("wrote", p["file"], f"({len(nb['cells'])} cells)")
