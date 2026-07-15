# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
BINARY_NAME=busyscout
RELEASE_DIR=releases

# Get version from git tag
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Platforms
PLATFORMS := windows linux darwin
ARCHITECTURES := amd64

all: clean test build

local:
	$(GOBUILD) -o $(BINARY_NAME) .

test:
	$(GOTEST) -v ./...

clean:
	$(GOCLEAN)
	rm -rf $(RELEASE_DIR)

build:
	mkdir -p $(RELEASE_DIR)
	$(foreach GOOS, $(PLATFORMS),\
		$(foreach GOARCH, $(ARCHITECTURES),\
			$(shell export GOOS=$(GOOS); export GOARCH=$(GOARCH); $(GOBUILD) -ldflags '-s -X main.Version=$(VERSION)' -o $(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH))))

# Helper cross-compilation via Docker toolchains (ipeye-cloud-minimal-toolchains)
# Requires Docker. Images are pulled on first use.
# Toolchains: https://github.com/eafilin/ipeye-cloud-minimal-toolchains
#
# Builds DYNAMICALLY linked helpers (~4-8 KB each). Since Phase 1 already detects
# the libc family, we upload only the matching helper. This is 30-50x smaller
# than static + UPX binaries.
#
# Per-ISA helpers with libc variant in filename: elfreader-<isa>-<libc>
#   arm-uclibc, arm-glibc, arm-musl
#   aarch64-glibc
#   mipsel-uclibc, mips-uclibc
#   x86-glibc, x86_64-glibc
HELPER_SRC=internal/helpers/src/elfreader.c
HELPER_BIN_DIR=internal/helpers/bin
HELPER_WORKDIR=/workspace
CFLAGS_COMMON=-std=c99 -s -Os

helpers: helpers-arm helpers-aarch64 helpers-mipsel helpers-mips helpers-x86 helpers-x86_64
	@echo "=== Helpers built ==="
	@ls -lh $(HELPER_BIN_DIR)/elfreader-*

# --- ARM32: uClibc (HiSilicon), glibc (Cortex-A9), musl ---

helpers-arm:
	mkdir -p $(HELPER_BIN_DIR)
	@# uClibc soft-float — HiSilicon Hi3516/Hi3518, broadest ARM camera coverage
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/arm-hisiv500-linux \
		arm-hisiv500-linux-uclibcgnueabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-arm-uclibc $(HELPER_WORKDIR)/$(HELPER_SRC)
	@# glibc hard-float — newer ARM cameras
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/arm-ca9-linux-gnueabihf-6.5 \
		arm-ca9-linux-gnueabihf-gcc $(CFLAGS_COMMON) -march=armv5te \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-arm-glibc $(HELPER_WORKDIR)/$(HELPER_SRC)
	@# musl soft-float
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/arm-gcc7.3-linux-musleabi \
		arm-gcc7.3-linux-musleabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-arm-musl $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- AArch64: glibc ---

helpers-aarch64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/aarch64-mix210-linux \
		aarch64-mix210-linux-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-aarch64-glibc $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- MIPS32 LE: uClibc ---

helpers-mipsel:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) -EL \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-mipsel-uclibc $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- MIPS32 BE: uClibc ---

helpers-mips:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		efilin/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-mips-uclibc $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- x86 / x86_64: glibc (via ubuntu Docker) ---

helpers-x86:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		ubuntu:22.04 bash -c '\
			apt-get update -qq && apt-get install -y -qq gcc-multilib && \
			gcc -std=c99 -s -Os -m32 \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-x86-glibc $(HELPER_WORKDIR)/$(HELPER_SRC)'

helpers-x86_64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		ubuntu:22.04 bash -c '\
			apt-get update -qq && apt-get install -y -qq gcc && \
			gcc -std=c99 -s -Os \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-x86_64-glibc $(HELPER_WORKDIR)/$(HELPER_SRC)'

helpers-clean:
	rm -f $(HELPER_BIN_DIR)/elfreader-*
	touch $(HELPER_BIN_DIR)/elfreader-arm-uclibc
	touch $(HELPER_BIN_DIR)/elfreader-arm-glibc
	touch $(HELPER_BIN_DIR)/elfreader-arm-musl
	touch $(HELPER_BIN_DIR)/elfreader-aarch64-glibc
	touch $(HELPER_BIN_DIR)/elfreader-mipsel-uclibc
	touch $(HELPER_BIN_DIR)/elfreader-mips-uclibc
	touch $(HELPER_BIN_DIR)/elfreader-x86-glibc
	touch $(HELPER_BIN_DIR)/elfreader-x86_64-glibc

# wistic/telnetd default credentials are user:password
test-integration-detect:
	docker compose -f tests/docker-compose.yaml up -d
	sleep 2
	go run . detect user:password@127.0.0.1:2323
	docker compose -f tests/docker-compose.yaml down

.PHONY: all local test clean build helpers helpers-clean test-integration-detect \
        helpers-arm helpers-aarch64 helpers-mipsel helpers-mips helpers-x86 helpers-x86_64
