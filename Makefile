.PHONY: build clean run test install vet lint

# Binary name
BINARY_NAME=cray
BUILD_DIR=bin
LAUNCHER_DIR=cmd/cray-launcher
LAUNCHER_EMBED_DIR=$(LAUNCHER_DIR)/embedded

# Version
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Build flags
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)"

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/cray

clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)

run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME)

test: ensure-embed-placeholder
	@echo "Running tests..."
	$(GOTEST) -v ./...

deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

install: build
	@echo "Installing $(BINARY_NAME)..."
	cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/

uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	rm -f /usr/local/bin/$(BINARY_NAME)

# Development
dev: build
	@echo "Running in development mode..."
	./$(BUILD_DIR)/$(BINARY_NAME)

# Cross compilation — Linux
build-linux:
	@echo "Building for Linux amd64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/cray

build-linux-arm64:
	@echo "Building for Linux arm64..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/cray

# Static Linux builds used for embedding inside Darwin launcher.
# CGO_ENABLED=0 ensures the binary works after chroot into the Docker Desktop VM root.
build-linux-static-amd64:
	@echo "Building static Linux amd64..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/cray

build-linux-static-arm64:
	@echo "Building static Linux arm64..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/cray

# Cross compilation — Darwin (launcher that embeds the Linux binary)
# The launcher runs on macOS and transparently executes the real cray
# inside a privileged helper container with chroot into the Docker Desktop VM.
build-darwin: build-darwin-arm64

build-darwin-arm64: build-linux-static-arm64
	@echo "Building Darwin arm64 launcher (embedded Linux arm64)..."
	@mkdir -p $(LAUNCHER_EMBED_DIR)
	@cp $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(LAUNCHER_EMBED_DIR)/cray-linux
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(LAUNCHER_DIR)
	@printf '#!/bin/sh\necho "error: placeholder binary - rebuild with: make build-darwin"\nexit 1\n' > $(LAUNCHER_EMBED_DIR)/cray-linux

build-darwin-amd64: build-linux-static-amd64
	@echo "Building Darwin amd64 launcher (embedded Linux amd64)..."
	@mkdir -p $(LAUNCHER_EMBED_DIR)
	@cp $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(LAUNCHER_EMBED_DIR)/cray-linux
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./$(LAUNCHER_DIR)
	@printf '#!/bin/sh\necho "error: placeholder binary - rebuild with: make build-darwin"\nexit 1\n' > $(LAUNCHER_EMBED_DIR)/cray-linux

# Build the raw (non-launcher) Darwin binary, for testing only.
build-darwin-native:
	@echo "Building native Darwin arm64 (no launcher)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64-native ./cmd/cray

# Ensure the embed placeholder exists so that `go vet ./...` passes
# even when the real embedded binary has not been built yet.
ensure-embed-placeholder:
	@mkdir -p $(LAUNCHER_EMBED_DIR)
	@test -f $(LAUNCHER_EMBED_DIR)/cray-linux || printf '#!/bin/sh\necho "error: placeholder binary - rebuild with: make build-darwin"\nexit 1\n' > $(LAUNCHER_EMBED_DIR)/cray-linux

build-all: build-linux build-linux-arm64 build-darwin-arm64 build-darwin-amd64

help:
	@echo "Available targets:"
	@echo "  build        - Build the binary"
	@echo "  clean        - Clean build artifacts"
	@echo "  run          - Build and run the application"
	@echo "  test         - Run tests"
	@echo "  deps         - Download and tidy dependencies"
	@echo "  install      - Install binary to /usr/local/bin"
	@echo "  uninstall    - Remove binary from /usr/local/bin"
	@echo "  dev          - Run in development mode"
	@echo "  build-linux  - Cross compile for Linux"
	@echo "  build-darwin - Cross compile for macOS"
	@echo "  build-all    - Cross compile for all platforms"
