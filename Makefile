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

all: clean test helpers fileloaders build

local: helpers-clean fileloaders-clean
	@echo "WARNING: building with stub helpers — detect/xfer will fail at runtime"
	$(GOBUILD) -o $(BINARY_NAME) .

local-full: helpers fileloaders
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
FILELOADER_SRC=internal/helpers/src/fileloader.c
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
		krabiswabbie/arm-hisiv500-linux \
		arm-hisiv500-linux-uclibcgnueabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-arm-uclibc $(HELPER_WORKDIR)/$(HELPER_SRC)
	@# glibc hard-float — newer ARM cameras
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/arm-ca9-linux-gnueabihf-6.5 \
		arm-ca9-linux-gnueabihf-gcc $(CFLAGS_COMMON) -march=armv5te \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-arm-glibc $(HELPER_WORKDIR)/$(HELPER_SRC)
	@# musl soft-float
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/arm-gcc7.3-linux-musleabi \
		arm-gcc7.3-linux-musleabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-arm-musl $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- AArch64: glibc ---

helpers-aarch64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/aarch64-mix210-linux \
		aarch64-mix210-linux-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-aarch64-glibc $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- MIPS32 LE: uClibc ---

helpers-mipsel:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) -EL \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/elfreader-mipsel-uclibc $(HELPER_WORKDIR)/$(HELPER_SRC)

# --- MIPS32 BE: uClibc ---

helpers-mips:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/mips-gcc720-uclibc229-r519 \
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
	mkdir -p $(HELPER_BIN_DIR)
	rm -f $(HELPER_BIN_DIR)/elfreader-*
	touch $(HELPER_BIN_DIR)/elfreader-arm-uclibc
	touch $(HELPER_BIN_DIR)/elfreader-arm-glibc
	touch $(HELPER_BIN_DIR)/elfreader-arm-musl
	touch $(HELPER_BIN_DIR)/elfreader-aarch64-glibc
	touch $(HELPER_BIN_DIR)/elfreader-mipsel-uclibc
	touch $(HELPER_BIN_DIR)/elfreader-mips-uclibc
	touch $(HELPER_BIN_DIR)/elfreader-x86-glibc
	touch $(HELPER_BIN_DIR)/elfreader-x86_64-glibc

# --- Fileloader (fast file transfer) ---

fileloaders: fileloaders-arm fileloaders-aarch64 fileloaders-mipsel fileloaders-mips fileloaders-x86 fileloaders-x86_64 fileloaders-x86_64-musl fileloaders-x86_64-static
	@echo "=== Fileloaders built ==="
	@ls -lh $(HELPER_BIN_DIR)/fileloader-*

fileloaders-arm:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/arm-hisiv500-linux \
		arm-hisiv500-linux-uclibcgnueabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-arm-uclibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/arm-ca9-linux-gnueabihf-6.5 \
		arm-ca9-linux-gnueabihf-gcc $(CFLAGS_COMMON) -march=armv5te \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-arm-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/arm-gcc7.3-linux-musleabi \
		arm-gcc7.3-linux-musleabi-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-arm-musl $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-aarch64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/aarch64-mix210-linux \
		aarch64-mix210-linux-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-aarch64-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-mipsel:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) -EL \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-mipsel-uclibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-mips:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		krabiswabbie/mips-gcc720-uclibc229-r519 \
		mips-linux-uclibc-gcc $(CFLAGS_COMMON) \
		-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-mips-uclibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)

fileloaders-x86:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		ubuntu:22.04 bash -c '\
			apt-get update -qq && apt-get install -y -qq gcc-multilib && \
			gcc -std=c99 -s -Os -m32 \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-x86-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)'

fileloaders-x86_64:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		ubuntu:22.04 bash -c '\
			apt-get update -qq && apt-get install -y -qq gcc && \
			gcc -std=c99 -s -Os \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-x86_64-glibc $(HELPER_WORKDIR)/$(FILELOADER_SRC)'

fileloaders-x86_64-musl:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		alpine:3.20 sh -c '\
			apk add --no-cache gcc musl-dev && \
			gcc -std=c99 -s -Os \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-x86_64-musl $(HELPER_WORKDIR)/$(FILELOADER_SRC)'

fileloaders-x86_64-static:
	mkdir -p $(HELPER_BIN_DIR)
	docker run --platform linux/amd64 --rm \
		-v "$(shell pwd):$(HELPER_WORKDIR)" \
		alpine:3.20 sh -c '\
			apk add --no-cache gcc musl-dev && \
			gcc -std=c99 -s -Os -static \
				-o $(HELPER_WORKDIR)/$(HELPER_BIN_DIR)/fileloader-x86_64-static $(HELPER_WORKDIR)/$(FILELOADER_SRC)'

fileloaders-clean:
	mkdir -p $(HELPER_BIN_DIR)
	rm -f $(HELPER_BIN_DIR)/fileloader-*
	touch $(HELPER_BIN_DIR)/fileloader-arm-uclibc
	touch $(HELPER_BIN_DIR)/fileloader-arm-glibc
	touch $(HELPER_BIN_DIR)/fileloader-arm-musl
	touch $(HELPER_BIN_DIR)/fileloader-aarch64-glibc
	touch $(HELPER_BIN_DIR)/fileloader-mipsel-uclibc
	touch $(HELPER_BIN_DIR)/fileloader-mips-uclibc
	touch $(HELPER_BIN_DIR)/fileloader-x86-glibc
	touch $(HELPER_BIN_DIR)/fileloader-x86_64-glibc
	touch $(HELPER_BIN_DIR)/fileloader-x86_64-musl
	touch $(HELPER_BIN_DIR)/fileloader-x86_64-static

# --- Integration tests (multi-ISA) ---
#
# Containers (tests/docker-compose.yaml):
#   telnet-x86_64  — x86_64 glibc (port 2323)
#   telnet-aarch64 — aarch64 glibc (port 2324, native on Apple Silicon)
#   telnet-arm     — arm32 musl   (port 2325, requires QEMU on non-ARM)
#
# All containers use user:password.

# QEMU setup for ARM emulation on x86_64 hosts
qemu-arm-setup:
	@if [ "$$(uname -m)" != "aarch64" ] && [ "$$(uname -m)" != "arm64" ]; then \
		docker run --rm --privileged aptible/qemu-user-static --reset 2>/dev/null || true; \
	fi

# Detect test — x86_64 glibc container (port 2323)
test-integration-detect: local-full
	docker compose -f tests/docker-compose.yaml up telnet-x86_64 -d
	sleep 2
	./$(BINARY_NAME) detect user:password@127.0.0.1:2323:/
	docker compose -f tests/docker-compose.yaml down

# Fast file transfer — x86_64 glibc (port 2323, always available)
test-integration-xfer-x86_64: local-full
	docker compose -f tests/docker-compose.yaml up telnet-x86_64 -d
	sleep 2
	bash tests/integration_xfer_test.sh ./busyscout 2323 x86_64-glibc
	docker compose -f tests/docker-compose.yaml down

# Fast file transfer — aarch64 glibc (port 2324, native on Apple Silicon)
test-integration-xfer-aarch64: local-full
	docker compose -f tests/docker-compose.yaml up telnet-aarch64 -d
	sleep 2
	bash tests/integration_xfer_test.sh ./busyscout 2324 aarch64-glibc
	docker compose -f tests/docker-compose.yaml down

# Fast file transfer — arm32 musl (port 2325, requires QEMU)
test-integration-xfer-arm: local-full qemu-arm-setup
	docker compose -f tests/docker-compose.yaml up telnet-arm -d
	sleep 2
	bash tests/integration_xfer_test.sh ./busyscout 2325 arm-musl
	docker compose -f tests/docker-compose.yaml down

# All fast file transfer tests
test-integration-xfer: test-integration-xfer-x86_64

.PHONY: all local local-full test clean build helpers helpers-clean \
        test-integration-detect test-integration-xfer \
        test-integration-xfer-x86_64 test-integration-xfer-aarch64 test-integration-xfer-arm \
        qemu-arm-setup \
        helpers-arm helpers-aarch64 helpers-mipsel helpers-mips helpers-x86 helpers-x86_64 \
        fileloaders fileloaders-arm fileloaders-aarch64 fileloaders-mipsel fileloaders-mips fileloaders-x86 fileloaders-x86_64 fileloaders-x86_64-musl fileloaders-x86_64-static fileloaders-clean
