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

# Helper cross-compilation
HELPER_SRC=internal/helpers/src/elfreader.c
HELPER_BIN_DIR=internal/helpers/bin

helpers:
	mkdir -p $(HELPER_BIN_DIR)
	arm-linux-gnueabi-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-arm $(HELPER_SRC)
	aarch64-linux-gnu-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-aarch64 $(HELPER_SRC)
	mipsel-linux-gnu-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-mipsel $(HELPER_SRC)
	mips-linux-gnu-gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-mips $(HELPER_SRC)
	gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-x86 $(HELPER_SRC)
	gcc -static -s -Os -o $(HELPER_BIN_DIR)/elfreader-x86_64 $(HELPER_SRC)
	@echo "Helpers built successfully"

helpers-clean:
	rm -f $(HELPER_BIN_DIR)/elfreader-*

.PHONY: all test clean build helpers helpers-clean