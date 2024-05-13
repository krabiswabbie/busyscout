# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
BINARY_NAME=busyscout
RELEASE_DIR=releases

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
	$(foreach GOOS, $(PLATFORMS),\
		$(foreach GOARCH, $(ARCHITECTURES),\
			$(shell export GOOS=$(GOOS); export GOARCH=$(GOARCH); $(GOBUILD) -ldflags '-s' -o $(RELEASE_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH))))

.PHONY: all test clean build
