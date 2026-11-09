# Builds the Assay CUDA probes into probes/bin/.
#
#   make                    build gemm, membw, soak, memtest for the GPU in this machine
#   make allreduce          also build the multi-GPU probe (needs NCCL)
#   make ARCH=sm_75         build for a specific GPU generation (no GPU needed, used by CI)
#   make NCCL_HOME=/path    use NCCL from a custom location (e.g. the pip nvidia-nccl package)
#
# GPU generations: sm_60 = P100, sm_70 = V100, sm_75 = T4, sm_80 = A100, sm_90 = H100.

NVCC      ?= nvcc
ARCH      ?= native
NVCCFLAGS ?= -O3 -std=c++17 -arch=$(ARCH)
BIN       := bin

PROBES := gemm membw soak memtest

ifdef NCCL_HOME
NCCL_FLAGS := -I$(NCCL_HOME)/include -L$(NCCL_HOME)/lib -Xlinker -rpath=$(NCCL_HOME)/lib
endif
NCCL_LIB ?= -lnccl

.PHONY: all allreduce clean
all: $(addprefix $(BIN)/,$(PROBES))
allreduce: $(BIN)/allreduce

$(BIN):
	mkdir -p $(BIN)

$(BIN)/gemm: gemm.cu gemm.h common.h | $(BIN)
	$(NVCC) $(NVCCFLAGS) -o $@ $< -lcublas

$(BIN)/soak: soak.cu gemm.h common.h | $(BIN)
	$(NVCC) $(NVCCFLAGS) -o $@ $< -lcublas

$(BIN)/membw: membw.cu common.h | $(BIN)
	$(NVCC) $(NVCCFLAGS) -o $@ $<

$(BIN)/memtest: memtest.cu common.h | $(BIN)
	$(NVCC) $(NVCCFLAGS) -o $@ $<

$(BIN)/allreduce: allreduce.cu common.h | $(BIN)
	$(NVCC) $(NVCCFLAGS) $(NCCL_FLAGS) -o $@ $< $(NCCL_LIB)

clean:
	rm -rf $(BIN)
