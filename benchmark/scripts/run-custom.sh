#!/usr/bin/env bash
#
# Custom Go Benchmark Runner
# Runs custom Go benchmarks against JOG or MinIO
#
# Usage:
#   ./run-custom.sh [target]
#
# Arguments:
#   target - jog or minio (default: jog)
#

set -euo pipefail

# Configuration
JOG_ENDPOINT="http://localhost:9200"
MINIO_ENDPOINT="http://localhost:9300"
RCLONE_ENDPOINT="http://localhost:9400"
VERSITYGW_ENDPOINT="http://localhost:9500"
ACCESS_KEY="benchadmin"
SECRET_KEY="benchadmin"
RESULTS_DIR="benchmark/results"
BENCHMARK_DIR="benchmark/custom"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print colored message
log_info() {
    echo -e "${GREEN}[INFO]${NC} $@"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $@"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $@"
}

# Show help message
show_help() {
    cat << EOF
Custom Go Benchmark Runner

Usage:
  $(basename "$0") [target]

Arguments:
  target    Target server to benchmark (default: jog)
            - jog        : Benchmark JOG
            - minio      : Benchmark MinIO
            - rclone     : Benchmark rclone serve s3
            - versitygw  : Benchmark Versity Gateway

Configuration:
  JOG endpoint        : ${JOG_ENDPOINT}
  MinIO endpoint      : ${MINIO_ENDPOINT}
  rclone endpoint     : ${RCLONE_ENDPOINT}
  versitygw endpoint  : ${VERSITYGW_ENDPOINT}
  Credentials         : ${ACCESS_KEY}/${SECRET_KEY}
  Results directory   : ${RESULTS_DIR}
  Benchmark source    : ${BENCHMARK_DIR}

Examples:
  $(basename "$0")            # Run benchmarks against JOG
  $(basename "$0") jog        # Run benchmarks against JOG
  $(basename "$0") minio      # Run benchmarks against MinIO
  $(basename "$0") rclone     # Run benchmarks against rclone
  $(basename "$0") versitygw  # Run benchmarks against versitygw

Output:
  Results are saved in benchstat format for easy comparison:
  - ${RESULTS_DIR}/custom_jog_<timestamp>.txt
  - ${RESULTS_DIR}/custom_minio_<timestamp>.txt
  - ${RESULTS_DIR}/custom_rclone_<timestamp>.txt
  - ${RESULTS_DIR}/custom_versitygw_<timestamp>.txt

  Compare results with benchstat:
  $ benchstat custom_jog_*.txt custom_minio_*.txt custom_rclone_*.txt custom_versitygw_*.txt

EOF
}

# Check if Go is installed
check_go() {
    if ! command -v go &> /dev/null; then
        log_error "Go is not installed"
        echo ""
        echo "Please install Go from: https://golang.org/dl/"
        exit 1
    fi
    log_info "Go found: $(go version)"
}

# Check if benchmark directory exists
check_benchmark_dir() {
    if [[ ! -d "${BENCHMARK_DIR}" ]]; then
        log_error "Benchmark directory not found: ${BENCHMARK_DIR}"
        echo ""
        echo "Please create custom benchmarks in ${BENCHMARK_DIR}"
        exit 1
    fi

    # Check if there are any Go test files
    if ! ls "${BENCHMARK_DIR}"/*_test.go &> /dev/null; then
        log_error "No benchmark test files found in ${BENCHMARK_DIR}"
        echo ""
        echo "Please create benchmark test files (*_test.go) in ${BENCHMARK_DIR}"
        exit 1
    fi

    log_info "Benchmark directory: ${BENCHMARK_DIR}"
}

# Create results directory
prepare_results_dir() {
    mkdir -p "${RESULTS_DIR}"
    log_info "Results directory: ${RESULTS_DIR}"
}

# Run Go benchmarks
run_go_benchmarks() {
    local target=$1
    local endpoint=""
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local result_file="${RESULTS_DIR}/custom_${target}_${timestamp}.txt"

    case "${target}" in
        jog)
            endpoint="${JOG_ENDPOINT}"
            ;;
        minio)
            endpoint="${MINIO_ENDPOINT}"
            ;;
        rclone)
            endpoint="${RCLONE_ENDPOINT}"
            ;;
        versitygw)
            endpoint="${VERSITYGW_ENDPOINT}"
            ;;
        *)
            log_error "Unknown target: ${target}"
            return 1
            ;;
    esac

    log_info "Running custom Go benchmarks on ${target}..."
    log_info "Endpoint: ${endpoint}"

    # Set environment variables for benchmarks
    export BENCHMARK_ENDPOINT="${endpoint}"
    export BENCHMARK_ACCESS_KEY="${ACCESS_KEY}"
    export BENCHMARK_SECRET_KEY="${SECRET_KEY}"
    export BENCHMARK_TARGET="${target}"

    log_info "Environment variables set:"
    log_info "  BENCHMARK_ENDPOINT=${BENCHMARK_ENDPOINT}"
    log_info "  BENCHMARK_ACCESS_KEY=${BENCHMARK_ACCESS_KEY}"
    log_info "  BENCHMARK_TARGET=${BENCHMARK_TARGET}"

    # Run benchmarks
    log_info "Running benchmarks (this may take several minutes)..."

    # Convert result_file to absolute path
    local abs_result_file
    abs_result_file="$(pwd)/${result_file}"
    mkdir -p "$(dirname "${abs_result_file}")"

    # Run with benchstat-compatible output
    go test -bench=. -benchmem -benchtime=10s -timeout=30m "./${BENCHMARK_DIR}/..." > "${abs_result_file}" 2>&1 || {
        log_error "Benchmark execution failed"
        log_error "Check ${result_file} for details"
        return 1
    }

    log_info "Results saved: ${result_file}"

    # Show summary
    log_info "Benchmark summary:"
    grep "^Benchmark" "${result_file}" | head -n 10 || true

    # Unset environment variables
    unset BENCHMARK_ENDPOINT
    unset BENCHMARK_ACCESS_KEY
    unset BENCHMARK_SECRET_KEY
    unset BENCHMARK_TARGET
}

# Check if benchstat is available
check_benchstat() {
    if command -v benchstat &> /dev/null; then
        log_info "benchstat is available for result comparison"
        log_info "Compare results: benchstat ${RESULTS_DIR}/custom_jog_*.txt ${RESULTS_DIR}/custom_minio_*.txt"
    else
        log_warn "benchstat not found (optional)"
        log_info "Install with: go install golang.org/x/perf/cmd/benchstat@latest"
    fi
}

# Main function
main() {
    local target="${1:-jog}"

    # Show help if requested
    if [[ "${target}" == "-h" ]] || [[ "${target}" == "--help" ]]; then
        show_help
        exit 0
    fi

    # Validate arguments
    if [[ ! "${target}" =~ ^(jog|minio|rclone|versitygw)$ ]]; then
        log_error "Invalid target: ${target}"
        echo "Valid targets: jog, minio, rclone, versitygw"
        exit 1
    fi

    log_info "Starting custom Go benchmarks..."
    log_info "Target: ${target}"

    check_go
    check_benchmark_dir
    prepare_results_dir

    run_go_benchmarks "${target}"

    log_info "Benchmarks completed!"
    log_info "Results saved in: ${RESULTS_DIR}"

    check_benchstat
}

main "$@"
