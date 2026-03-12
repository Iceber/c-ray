#!/bin/bash

# c-ray Test Script for kind-control-plane
# This script builds the binary and tests it inside kind-control-plane container

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
CONTAINER_NAME="kind-control-plane"
BINARY_NAME="cray"
BUILD_DIR="bin"
INSTALL_PATH="/usr/local/bin/cray"

echo -e "${GREEN}=== c-ray Test Script ===${NC}"

# Check if kind-control-plane is running
echo -e "\n${YELLOW}[1/7] Checking kind-control-plane container...${NC}"
if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    echo -e "${RED}Error: ${CONTAINER_NAME} container is not running${NC}"
    echo "Please start kind cluster first: kind create cluster"
    exit 1
fi
echo -e "${GREEN}✓ Container is running${NC}"

# Detect architecture
echo -e "\n${YELLOW}[2/7] Detecting architecture...${NC}"
ARCH=$(docker exec ${CONTAINER_NAME} uname -m)
echo -e "${GREEN}✓ Architecture: ${ARCH}${NC}"

# Determine target architecture
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    TARGET_ARCH="arm64"
elif [ "$ARCH" = "x86_64" ]; then
    TARGET_ARCH="amd64"
else
    echo -e "${RED}Unsupported architecture: ${ARCH}${NC}"
    exit 1
fi

# Build the binary for Linux
echo -e "\n${YELLOW}[3/7] Building cray binary for Linux/${TARGET_ARCH}...${NC}"
GOOS=linux GOARCH=${TARGET_ARCH} go build -o ${BUILD_DIR}/${BINARY_NAME}-linux ./cmd/cray
if [ ! -f "${BUILD_DIR}/${BINARY_NAME}-linux" ]; then
    echo -e "${RED}Error: Build failed${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Build successful${NC}"

# Copy binary to container
echo -e "\n${YELLOW}[4/7] Copying binary to ${CONTAINER_NAME}...${NC}"
cat ${BUILD_DIR}/${BINARY_NAME}-linux | docker exec -i ${CONTAINER_NAME} bash -c "cat > ${INSTALL_PATH} && chmod +x ${INSTALL_PATH}"
echo -e "${GREEN}✓ Binary installed to ${INSTALL_PATH}${NC}"
