#!/usr/bin/env bash
#
# Warp Benchmark Runner
# Runs S3 performance benchmarks using MinIO Warp against JOG and/or MinIO
#
# Usage:
#   ./run-warp.sh [target] [scenario]
#
# Arguments:
#   target   - jog, minio, or both (default: both)
#   scenario - throughput, concurrency, mixed, or all (default: all)
#

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_DIR="$(dirname "${SCRIPT_DIR}")"

# Configuration
JOG_ENDPOINT="localhost:9200"
MINIO_ENDPOINT="localhost:9300"
ACCESS_KEY="benchadmin"
SECRET_KEY="benchadmin"
RESULTS_DIR="benchmark/results"
BUCKET_NAME="warp-benchmark"

# Warp binary (prefer local binary)
WARP_BIN="${BENCHMARK_DIR}/bin/warp"
if [[ ! -x "${WARP_BIN}" ]]; then
    WARP_BIN="warp"  # Fallback to system warp
fi

# Object size scenarios (in bytes)
OBJECT_SIZES=(1024 65536 1048576 16777216 67108864)
OBJECT_SIZE_LABELS=("1KB" "64KB" "1MB" "16MB" "64MB")

# Concurrency scenarios
CONCURRENCY_LEVELS=(1 4 8 16 32 64)

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
Warp Benchmark Runner

Usage:
  $(basename "$0") [target] [scenario]

Arguments:
  target    Target server to benchmark (default: both)
            - jog      : Benchmark JOG only
            - minio    : Benchmark MinIO only
            - both     : Benchmark both servers

  scenario  Benchmark scenario to run (default: all)
            - throughput   : Test different object sizes
            - concurrency  : Test different concurrency levels
            - mixed        : Mixed workload (70% GET, 30% PUT)
            - all          : Run all scenarios

Configuration:
  JOG endpoint      : ${JOG_ENDPOINT}
  MinIO endpoint    : ${MINIO_ENDPOINT}
  Credentials       : ${ACCESS_KEY}/${SECRET_KEY}
  Results directory : ${RESULTS_DIR}

Examples:
  $(basename "$0")                    # Run all scenarios on both servers
  $(basename "$0") jog throughput     # Run throughput test on JOG only
  $(basename "$0") minio concurrency  # Run concurrency test on MinIO only

EOF
}

# Check if warp is installed
check_warp() {
    if [[ ! -x "${WARP_BIN}" ]] && ! command -v warp &> /dev/null; then
        log_error "warp CLI is not installed"
        echo ""
        echo "Please install warp using one of the following methods:"
        echo ""
        echo "  Option 1 (Recommended): Download to benchmark/bin/"
        echo "    ./benchmark/scripts/install-warp.sh"
        echo ""
        echo "  Option 2: Install globally"
        echo "    macOS:   brew install minio/stable/warp"
        echo "    Linux:   Download from https://github.com/minio/warp/releases"
        echo "    Go:      go install github.com/minio/warp@latest"
        exit 1
    fi
    log_info "warp CLI found: $(${WARP_BIN} --version 2>&1 | head -n1)"
}

# Create results directory
prepare_results_dir() {
    mkdir -p "${RESULTS_DIR}"
    log_info "Results directory: ${RESULTS_DIR}"
}

# Run throughput benchmark
run_throughput_benchmark() {
    local target=$1
    local endpoint=$2
    local timestamp=$(date +%Y%m%d_%H%M%S)

    log_info "Running throughput benchmark on ${target}..."

    for i in "${!OBJECT_SIZES[@]}"; do
        local size=${OBJECT_SIZES[$i]}
        local label=${OBJECT_SIZE_LABELS[$i]}
        local result_file="${RESULTS_DIR}/warp_${target}_throughput_${label}_${timestamp}.json"

        log_info "Testing object size: ${label} (${size} bytes)"

        ${WARP_BIN} get \
            --host="${endpoint}" \
            --access-key="${ACCESS_KEY}" \
            --secret-key="${SECRET_KEY}" \
            --bucket="${BUCKET_NAME}" \
            --obj.size="${size}" \
            --duration=30s \
            --concurrent=8 \
            --autoterm \
            --json > "${result_file}" 2>&1 || {
                log_warn "Benchmark failed for ${label}, continuing..."
                continue
            }

        log_info "Results saved: ${result_file}"
    done
}

# Run concurrency benchmark
run_concurrency_benchmark() {
    local target=$1
    local endpoint=$2
    local timestamp=$(date +%Y%m%d_%H%M%S)

    log_info "Running concurrency benchmark on ${target}..."

    for concurrency in "${CONCURRENCY_LEVELS[@]}"; do
        local result_file="${RESULTS_DIR}/warp_${target}_concurrency_${concurrency}_${timestamp}.json"

        log_info "Testing concurrency level: ${concurrency}"

        ${WARP_BIN} get \
            --host="${endpoint}" \
            --access-key="${ACCESS_KEY}" \
            --secret-key="${SECRET_KEY}" \
            --bucket="${BUCKET_NAME}" \
            --obj.size=1048576 \
            --duration=30s \
            --concurrent="${concurrency}" \
            --autoterm \
            --json > "${result_file}" 2>&1 || {
                log_warn "Benchmark failed for concurrency ${concurrency}, continuing..."
                continue
            }

        log_info "Results saved: ${result_file}"
    done
}

# Run mixed workload benchmark
run_mixed_benchmark() {
    local target=$1
    local endpoint=$2
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local result_file="${RESULTS_DIR}/warp_${target}_mixed_${timestamp}.json"

    log_info "Running mixed workload benchmark on ${target}..."
    log_info "Workload: 70% GET, 30% PUT"

    ${WARP_BIN} mixed \
        --host="${endpoint}" \
        --access-key="${ACCESS_KEY}" \
        --secret-key="${SECRET_KEY}" \
        --bucket="${BUCKET_NAME}" \
        --obj.size=1048576 \
        --duration=60s \
        --concurrent=16 \
        --get-distrib=70 \
        --put-distrib=30 \
        --autoterm \
        --json > "${result_file}" 2>&1 || {
            log_error "Mixed benchmark failed"
            return 1
        }

    log_info "Results saved: ${result_file}"
}

# Run benchmarks for a target
run_benchmarks() {
    local target=$1
    local scenario=$2
    local endpoint=""

    case "${target}" in
        jog)
            endpoint="${JOG_ENDPOINT}"
            ;;
        minio)
            endpoint="${MINIO_ENDPOINT}"
            ;;
        *)
            log_error "Unknown target: ${target}"
            return 1
            ;;
    esac

    log_info "Target: ${target} (${endpoint})"

    case "${scenario}" in
        throughput)
            run_throughput_benchmark "${target}" "${endpoint}"
            ;;
        concurrency)
            run_concurrency_benchmark "${target}" "${endpoint}"
            ;;
        mixed)
            run_mixed_benchmark "${target}" "${endpoint}"
            ;;
        all)
            run_throughput_benchmark "${target}" "${endpoint}"
            run_concurrency_benchmark "${target}" "${endpoint}"
            run_mixed_benchmark "${target}" "${endpoint}"
            ;;
        *)
            log_error "Unknown scenario: ${scenario}"
            return 1
            ;;
    esac
}

# Main function
main() {
    local target="${1:-both}"
    local scenario="${2:-all}"

    # Show help if requested
    if [[ "${target}" == "-h" ]] || [[ "${target}" == "--help" ]]; then
        show_help
        exit 0
    fi

    # Validate arguments
    if [[ ! "${target}" =~ ^(jog|minio|both)$ ]]; then
        log_error "Invalid target: ${target}"
        echo "Valid targets: jog, minio, both"
        exit 1
    fi

    if [[ ! "${scenario}" =~ ^(throughput|concurrency|mixed|all)$ ]]; then
        log_error "Invalid scenario: ${scenario}"
        echo "Valid scenarios: throughput, concurrency, mixed, all"
        exit 1
    fi

    log_info "Starting Warp benchmarks..."
    log_info "Target: ${target}, Scenario: ${scenario}"

    check_warp
    prepare_results_dir

    case "${target}" in
        jog)
            run_benchmarks "jog" "${scenario}"
            ;;
        minio)
            run_benchmarks "minio" "${scenario}"
            ;;
        both)
            run_benchmarks "jog" "${scenario}"
            run_benchmarks "minio" "${scenario}"
            ;;
    esac

    log_info "Benchmarks completed!"
    log_info "Results saved in: ${RESULTS_DIR}"
}

main "$@"
