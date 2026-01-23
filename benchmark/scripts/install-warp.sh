#!/usr/bin/env bash
#
# Warp Installation Script
# Downloads and installs MinIO Warp benchmark tool to benchmark/bin/
#
# Usage:
#   ./install-warp.sh [version]
#
# Arguments:
#   version - Warp version to install (default: v1.4.0)
#

set -euo pipefail

# Configuration
WARP_VERSION="${1:-v1.4.0}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_DIR="$(dirname "${SCRIPT_DIR}")"
BIN_DIR="${BENCHMARK_DIR}/bin"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $@"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $@"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $@"
}

# Detect OS and architecture
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux*)  os="linux" ;;
        Darwin*) os="darwin" ;;
        *)
            log_error "Unsupported operating system: $(uname -s)"
            exit 1
            ;;
    esac

    case "$(uname -m)" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        arm64)   arch="arm64" ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac

    echo "${os}-${arch}"
}

# Download warp binary
download_warp() {
    local platform="$1"
    local version="$2"
    local url="https://dl.min.io/aistor/warp/release/${platform}/archive/warp.${version}"

    log_info "Downloading Warp ${version} for ${platform}..."
    log_info "URL: ${url}"

    mkdir -p "${BIN_DIR}"

    if command -v curl &> /dev/null; then
        curl -L -o "${BIN_DIR}/warp" "${url}"
    elif command -v wget &> /dev/null; then
        wget -O "${BIN_DIR}/warp" "${url}"
    else
        log_error "Neither curl nor wget found. Please install one of them."
        exit 1
    fi

    chmod +x "${BIN_DIR}/warp"
    log_info "Warp installed to: ${BIN_DIR}/warp"
}

# Verify installation
verify_installation() {
    if "${BIN_DIR}/warp" --version &> /dev/null; then
        log_info "Installation verified: $(${BIN_DIR}/warp --version 2>&1 | head -n1)"
        return 0
    else
        log_error "Installation verification failed"
        return 1
    fi
}

# Show help
show_help() {
    cat << EOF
Warp Installation Script

Usage:
  $(basename "$0") [version]

Arguments:
  version   Warp version to install (default: v1.4.0)

Examples:
  $(basename "$0")          # Install v1.4.0
  $(basename "$0") v1.3.0   # Install v1.3.0

Supported Platforms:
  - linux-amd64
  - linux-arm64
  - darwin-amd64
  - darwin-arm64

The binary will be installed to: ${BIN_DIR}/warp

EOF
}

# Main
main() {
    if [[ "${1:-}" == "-h" ]] || [[ "${1:-}" == "--help" ]]; then
        show_help
        exit 0
    fi

    log_info "Installing MinIO Warp ${WARP_VERSION}..."

    local platform
    platform=$(detect_platform)
    log_info "Detected platform: ${platform}"

    download_warp "${platform}" "${WARP_VERSION}"
    verify_installation

    log_info "Installation complete!"
    log_info ""
    log_info "Usage:"
    log_info "  ${BIN_DIR}/warp --help"
    log_info "  ./benchmark/scripts/run-warp.sh both throughput"
}

main "$@"
