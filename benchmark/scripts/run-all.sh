#!/usr/bin/env bash
#
# JOG Benchmark Suite - All-in-One Runner
# Docker起動 → ベンチマーク実行 → Docker終了を一括で行う
#
# Usage:
#   ./run-all.sh [target] [scenario] [OPTIONS]
#
# Arguments:
#   target   - jog, minio, or both (default: both)
#   scenario - throughput, concurrency, mixed, or all (default: all)
#
# Options:
#   --skip-warp      Skip Warp benchmarks
#   --skip-custom    Skip custom Go benchmarks
#   --skip-report    Skip report generation
#   -k, --keep-running  Keep Docker containers running after completion
#   -c, --clean      Remove volumes before starting
#   --timeout SECS   Health check timeout in seconds (default: 120)
#   -h, --help       Show this help message
#

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_DIR="$(dirname "${SCRIPT_DIR}")"

# Default values
TARGET="both"
SCENARIO="all"
SKIP_WARP=false
SKIP_CUSTOM=false
SKIP_REPORT=false
KEEP_RUNNING=false
CLEAN_START=false
HEALTH_TIMEOUT=120

# Ports
JOG_PORT=9200
MINIO_PORT=9300

# Compose file
COMPOSE_FILE="${BENCHMARK_DIR}/docker-compose.benchmark.yml"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Start time for duration calculation
START_TIME=$(date +%s)

# Track status
FINAL_STATUS="SUCCESS"
WARNINGS=()

# Print colored message
log_info() {
    echo -e "  ${GREEN}✓${NC} $*"
}

log_warn() {
    echo -e "  ${YELLOW}⚠${NC} $*"
    WARNINGS+=("$*")
}

log_error() {
    echo -e "  ${RED}✗${NC} $*"
    FINAL_STATUS="FAILED"
}

log_phase() {
    echo ""
    echo -e "${CYAN}[Phase $1]${NC} ${BOLD}$2${NC}"
}

# Print header box
print_header() {
    local start_time=$(date '+%Y-%m-%d %H:%M')
    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║${NC}${BOLD}                 JOG Benchmark Suite                        ${NC}${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  Target: ${CYAN}${TARGET}${NC} | Scenario: ${CYAN}${SCENARIO}${NC} | Start: ${start_time}  ${BLUE}║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
}

# Print summary box
print_summary() {
    local end_time=$(date +%s)
    local duration=$((end_time - START_TIME))
    local minutes=$((duration / 60))
    local seconds=$((duration % 60))
    local duration_str="${minutes}m ${seconds}s"

    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║${NC}${BOLD}                       Summary                              ${NC}${BLUE}║${NC}"
    if [[ "${FINAL_STATUS}" == "SUCCESS" ]]; then
        echo -e "${BLUE}║${NC}  Status: ${GREEN}${FINAL_STATUS}${NC}                                           ${BLUE}║${NC}"
    else
        echo -e "${BLUE}║${NC}  Status: ${RED}${FINAL_STATUS}${NC}                                            ${BLUE}║${NC}"
    fi
    echo -e "${BLUE}║${NC}  Duration: ${duration_str}                                         ${BLUE}║${NC}"
    echo -e "${BLUE}║${NC}  Results: benchmark/results/                               ${BLUE}║${NC}"
    if [[ ! "${SKIP_REPORT}" == "true" ]]; then
        echo -e "${BLUE}║${NC}  Report: benchmark/results/REPORT.md                       ${BLUE}║${NC}"
    fi
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"

    if [[ ${#WARNINGS[@]} -gt 0 ]]; then
        echo ""
        echo -e "${YELLOW}Warnings:${NC}"
        for warning in "${WARNINGS[@]}"; do
            echo -e "  - ${warning}"
        done
    fi
}

# Show help message
show_help() {
    cat << EOF
JOG Benchmark Suite - All-in-One Runner

Usage:
  $(basename "$0") [target] [scenario] [OPTIONS]

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

Options:
  --skip-warp        Skip Warp benchmarks
  --skip-custom      Skip custom Go benchmarks
  --skip-report      Skip report generation
  -k, --keep-running Keep Docker containers running after completion
  -c, --clean        Remove volumes before starting (clean slate)
  --timeout SECS     Health check timeout in seconds (default: 120)
  -h, --help         Show this help message

Examples:
  $(basename "$0")                           # Full benchmark suite
  $(basename "$0") jog throughput            # JOG throughput test only
  $(basename "$0") both all --clean          # Clean start, full suite
  $(basename "$0") jog all -k                # Keep containers running
  $(basename "$0") both all --skip-custom    # Skip Go benchmarks

Environment:
  JOG endpoint   : localhost:${JOG_PORT}
  MinIO endpoint : localhost:${MINIO_PORT}
  Compose file   : ${COMPOSE_FILE}

EOF
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -h|--help)
                show_help
                exit 0
                ;;
            --skip-warp)
                SKIP_WARP=true
                shift
                ;;
            --skip-custom)
                SKIP_CUSTOM=true
                shift
                ;;
            --skip-report)
                SKIP_REPORT=true
                shift
                ;;
            -k|--keep-running)
                KEEP_RUNNING=true
                shift
                ;;
            -c|--clean)
                CLEAN_START=true
                shift
                ;;
            --timeout)
                HEALTH_TIMEOUT="$2"
                shift 2
                ;;
            jog|minio|both)
                TARGET="$1"
                shift
                ;;
            throughput|concurrency|mixed|all)
                SCENARIO="$1"
                shift
                ;;
            *)
                log_error "Unknown option: $1"
                echo "Use --help for usage information"
                exit 1
                ;;
        esac
    done
}

# Cleanup function for trap
cleanup() {
    if [[ "${KEEP_RUNNING}" == "false" ]]; then
        echo ""
        echo -e "${YELLOW}Cleaning up...${NC}"
        docker compose -f "${COMPOSE_FILE}" down --remove-orphans 2>/dev/null || true
    fi
}

# Check prerequisites
check_prerequisites() {
    log_phase "1/6" "Checking prerequisites..."

    # Check Docker
    if command -v docker &> /dev/null; then
        local docker_version=$(docker --version | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n1)
        log_info "Docker version ${docker_version}"
    else
        log_error "Docker is not installed"
        exit 1
    fi

    # Check Docker Compose
    if docker compose version &> /dev/null; then
        local compose_version=$(docker compose version --short 2>/dev/null || echo "unknown")
        log_info "Docker Compose version ${compose_version}"
    else
        log_error "Docker Compose is not available"
        exit 1
    fi

    # Check Warp CLI (only if not skipping)
    if [[ "${SKIP_WARP}" == "false" ]]; then
        local warp_bin="${BENCHMARK_DIR}/bin/warp"
        if [[ -x "${warp_bin}" ]]; then
            local warp_version=$(${warp_bin} --version 2>&1 | head -n1)
            log_info "Warp CLI: ${warp_version}"
        elif command -v warp &> /dev/null; then
            local warp_version=$(warp --version 2>&1 | head -n1)
            log_info "Warp CLI (system): ${warp_version}"
        else
            log_warn "Warp CLI not found, installing..."
            "${SCRIPT_DIR}/install-warp.sh" || {
                log_error "Failed to install Warp CLI"
                exit 1
            }
            log_info "Warp CLI installed: ${BENCHMARK_DIR}/bin/warp"
        fi
    else
        log_info "Warp benchmarks: skipped"
    fi

    # Check Go (only if not skipping custom benchmarks)
    if [[ "${SKIP_CUSTOM}" == "false" ]]; then
        if command -v go &> /dev/null; then
            local go_version=$(go version | grep -oE 'go[0-9]+\.[0-9]+(\.[0-9]+)?' | head -n1)
            log_info "Go version ${go_version}"
        else
            log_warn "Go is not installed, custom benchmarks will be skipped"
            SKIP_CUSTOM=true
        fi
    else
        log_info "Custom Go benchmarks: skipped"
    fi

    # Check port availability
    local port_conflict=false
    if lsof -i ":${JOG_PORT}" &> /dev/null; then
        log_warn "Port ${JOG_PORT} is already in use"
        port_conflict=true
    fi
    if lsof -i ":${MINIO_PORT}" &> /dev/null; then
        log_warn "Port ${MINIO_PORT} is already in use"
        port_conflict=true
    fi

    if [[ "${port_conflict}" == "false" ]]; then
        log_info "Ports ${JOG_PORT}, ${MINIO_PORT} available"
    fi
}

# Prepare environment
prepare_environment() {
    log_phase "2/6" "Preparing environment..."

    # Clean start if requested
    if [[ "${CLEAN_START}" == "true" ]]; then
        log_info "Removing existing containers and volumes..."
        docker compose -f "${COMPOSE_FILE}" down -v --remove-orphans 2>/dev/null || true
    fi

    # Create results directory
    mkdir -p "${BENCHMARK_DIR}/results"
    log_info "Results directory ready: benchmark/results/"
}

# Start Docker environment
start_docker() {
    log_phase "3/6" "Starting Docker environment..."

    # Start containers
    docker compose -f "${COMPOSE_FILE}" up -d

    # Wait for health checks
    local containers=()
    if [[ "${TARGET}" == "jog" ]] || [[ "${TARGET}" == "both" ]]; then
        containers+=("jog-benchmark")
    fi
    if [[ "${TARGET}" == "minio" ]] || [[ "${TARGET}" == "both" ]]; then
        containers+=("minio-benchmark")
    fi

    for container in "${containers[@]}"; do
        wait_for_healthy "${container}"
    done
}

# Wait for container to be healthy
wait_for_healthy() {
    local container=$1
    local elapsed=0
    local interval=5

    echo -ne "  \033[0;33m⠋\033[0m Waiting for ${container} to be healthy... (0s/${HEALTH_TIMEOUT}s)\r"

    while [[ ${elapsed} -lt ${HEALTH_TIMEOUT} ]]; do
        local status=$(docker inspect --format='{{.State.Health.Status}}' "${container}" 2>/dev/null || echo "not found")

        if [[ "${status}" == "healthy" ]]; then
            echo -e "  ${GREEN}✓${NC} ${container} is healthy                              "
            return 0
        fi

        sleep ${interval}
        elapsed=$((elapsed + interval))

        # Spinner animation
        local spinner_chars='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
        local spinner_index=$((elapsed / interval % ${#spinner_chars}))
        local spinner_char="${spinner_chars:${spinner_index}:1}"
        echo -ne "  \033[0;33m${spinner_char}\033[0m Waiting for ${container} to be healthy... (${elapsed}s/${HEALTH_TIMEOUT}s)\r"
    done

    echo -e "  ${RED}✗${NC} ${container} health check timeout                     "
    return 1
}

# Run Warp benchmarks
run_warp_benchmarks() {
    if [[ "${SKIP_WARP}" == "true" ]]; then
        return 0
    fi

    log_phase "4/6" "Running Warp benchmarks..."

    "${SCRIPT_DIR}/run-warp.sh" "${TARGET}" "${SCENARIO}" || {
        log_warn "Warp benchmarks had errors, continuing..."
    }
}

# Run custom Go benchmarks
run_custom_benchmarks() {
    if [[ "${SKIP_CUSTOM}" == "true" ]]; then
        log_phase "5/6" "Skipping custom Go benchmarks..."
        return 0
    fi

    log_phase "5/6" "Running custom Go benchmarks..."

    if [[ "${TARGET}" == "jog" ]] || [[ "${TARGET}" == "both" ]]; then
        "${SCRIPT_DIR}/run-custom.sh" jog || {
            log_warn "JOG custom benchmarks had errors, continuing..."
        }
    fi

    if [[ "${TARGET}" == "minio" ]] || [[ "${TARGET}" == "both" ]]; then
        "${SCRIPT_DIR}/run-custom.sh" minio || {
            log_warn "MinIO custom benchmarks had errors, continuing..."
        }
    fi
}

# Generate report
generate_report() {
    if [[ "${SKIP_REPORT}" == "true" ]]; then
        log_phase "6/6" "Skipping report generation..."
        return 0
    fi

    log_phase "6/6" "Generating report..."

    "${SCRIPT_DIR}/generate-report.sh" || {
        log_warn "Report generation had errors"
    }
}

# Stop Docker environment
stop_docker() {
    if [[ "${KEEP_RUNNING}" == "true" ]]; then
        echo ""
        log_info "Containers left running (--keep-running)"
        log_info "Stop with: docker compose -f ${COMPOSE_FILE} down"
    else
        echo ""
        log_info "Stopping Docker containers..."
        docker compose -f "${COMPOSE_FILE}" down --remove-orphans
    fi
}

# Main function
main() {
    parse_args "$@"

    # Set up trap for cleanup on interrupt
    trap cleanup EXIT INT TERM

    print_header

    check_prerequisites
    prepare_environment
    start_docker
    run_warp_benchmarks
    run_custom_benchmarks
    generate_report

    # Disable trap before normal exit
    trap - EXIT INT TERM
    stop_docker

    print_summary
}

main "$@"
