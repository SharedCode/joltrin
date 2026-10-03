#!/usr/bin/env bash
# scripts/demo.sh: Unified interactive demo and verification script for Joltrin (formerly SOP).
# Reproduces README terminal recordings and runs local protocol smoke tests.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# Configure colors if running in an interactive terminal.
if [[ -t 1 ]]; then
  BOLD='\033[1m'
  BLUE='\033[0;34m'
  CYAN='\033[0;36m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  RED='\033[0;31m'
  NC='\033[0m'
else
  BOLD=''
  BLUE=''
  CYAN=''
  GREEN=''
  YELLOW=''
  RED=''
  NC=''
fi

check_prerequisites() {
  if ! command -v go &>/dev/null; then
    echo -e "${RED}${BOLD}Error: 'go' binary not found in PATH.${NC}"
    echo "Joltrin requires Go 1.22+ to build and run."
    echo ""
    echo "Available options:"
    echo "  1. Install Go from https://go.dev/dl/ (or brew install go / apt install golang)"
    echo "  2. Run via Docker without a local Go toolchain:"
    echo "     docker run --rm -it -v \"\$PWD\":/src -w /src golang:1.26-alpine ./scripts/demo.sh"
    echo "  3. Pull the published quickstart container:"
    echo "     docker run --rm ghcr.io/sharedcode/joltrin-quickstart"
    exit 1
  fi
}

print_header() {
  local title="$1"
  echo ""
  echo -e "${BLUE}${BOLD}================================================================${NC}"
  echo -e "${CYAN}${BOLD}  ${title}${NC}"
  echo -e "${BLUE}${BOLD}================================================================${NC}"
  echo ""
}

run_barrier_demo() {
  print_header "Option 1: Verification Barrier Demo (Precedence Safety Check)"
  echo -e "${YELLOW}Demonstrating explicit-state precedence checking (verify).${NC}"
  echo -e "An agent attempting to drop prod is blocked until preconditions commit."
  echo ""
  go run ./examples/verify_barrier
  echo ""
  echo -e "${GREEN}✓ Precedence check barrier verified successfully.${NC}"
}

run_agent_memory_demo() {
  print_header "Option 2: AI Agent Memory Checkpoint & Resume Demo"
  echo -e "${YELLOW}Demonstrating B-Tree reasoning frame commits and failover.${NC}"
  echo -e "Simulates mid-reasoning worker failure, automatic rollback, and resume."
  echo ""
  go run ./examples/agent_memory
  echo ""
  echo -e "${GREEN}✓ Agent memory checkpoint and failover verified successfully.${NC}"
}

run_test_suite() {
  print_header "Option 3: Fast Engine Test Suite & Sanity Check"
  echo -e "${YELLOW}Running package tests across core storage, filesystem, and server...${NC}"
  echo ""
  go test ./inmemory ./btree ./common ./cache ./fs ./tools/httpserver ./tools/runbookstore
  echo ""
  echo -e "${GREEN}✓ Core engine test suites passed cleanly.${NC}"
}

run_protocol_smoke_test() {
  print_header "Option 4: Local MCP & A2A Protocol Reachability Probe"

  echo -e "${CYAN}${BOLD}[1/2] Probing MCP Server (cmd/sop-mcp-server) via JSON-RPC stdio...${NC}"
  local mcp_req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"sop-demo-probe","version":"1.0"}}}'
  local mcp_res
  mcp_res=$(echo "${mcp_req}" | go run ./cmd/sop-mcp-server)
  echo -e "MCP Response: ${GREEN}${mcp_res}${NC}"
  echo ""

  echo -e "${CYAN}${BOLD}[2/2] Probing A2A Agent (cmd/sop-a2a-agent) via HTTP agent card...${NC}"
  local test_port="8099"
  local agent_url="http://127.0.0.1:${test_port}"
  local tmp_bin
  tmp_bin=$(mktemp "${TMPDIR:-/tmp}/sop-a2a-probe.XXXXXX")

  # Build temporary binary to avoid go-run child process detachment
  go build -o "${tmp_bin}" ./cmd/sop-a2a-agent
  "${tmp_bin}" -addr "127.0.0.1:${test_port}" >/dev/null 2>&1 &
  local agent_pid=$!

  cleanup_agent() {
    if [[ -n "${agent_pid:-}" ]] && kill -0 "${agent_pid}" 2>/dev/null; then
      kill "${agent_pid}" 2>/dev/null || true
      wait "${agent_pid}" 2>/dev/null || true
    fi
    rm -f "${tmp_bin}" 2>/dev/null || true
  }
  trap cleanup_agent EXIT INT TERM

  local ready=0
  for _ in {1..25}; do
    if curl -s --connect-timeout 1 "${agent_url}/.well-known/agent-card.json" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 0.1
  done

  if [[ "${ready}" -eq 1 ]]; then
    local card
    card=$(curl -s "${agent_url}/.well-known/agent-card.json")
    echo -e "A2A Agent Card: ${GREEN}${card}${NC}"
    echo ""
    echo -e "${GREEN}✓ Both MCP and A2A protocols are responsive and reachable.${NC}"
  else
    echo -e "${RED}Error: Timed out waiting for A2A agent on ${agent_url}${NC}"
    cleanup_agent
    trap - EXIT INT TERM
    return 1
  fi

  cleanup_agent
  trap - EXIT INT TERM
}

run_all() {
  run_barrier_demo
  run_agent_memory_demo
  run_test_suite
  run_protocol_smoke_test
  echo ""
  echo -e "${GREEN}${BOLD}================================================================${NC}"
  echo -e "${GREEN}${BOLD}  All demonstrations and verification suites completed successfully!${NC}"
  echo -e "${GREEN}${BOLD}================================================================${NC}"
  echo ""
}

show_usage() {
  echo -e "${BOLD}Usage:${NC} $0 [option]"
  echo ""
  echo "Options:"
  echo "  -1, --barrier    Run Option 1: Verification Barrier precedence check demo"
  echo "  -2, --memory     Run Option 2: AI Agent memory checkpoint & failover demo"
  echo "  -3, --test       Run Option 3: Core engine and server unit test suites"
  echo "  -4, --protocol   Run Option 4: Local MCP JSON-RPC and A2A agent probe"
  echo "  -a, --all        Run all options sequentially (1 through 4)"
  echo "  -h, --help       Display this usage message"
  echo ""
  echo "Running without arguments starts the interactive selection menu."
}

interactive_menu() {
  while true; do
    echo ""
    echo -e "${BLUE}${BOLD}================================================================${NC}"
    echo -e "${CYAN}${BOLD}                Joltrin Interactive Demo Suite                  ${NC}"
    echo -e "${BLUE}${BOLD}================================================================${NC}"
    echo "Select an option to run:"
    echo "  1) Verification Barrier Demo (precondition precedence check)"
    echo "  2) AI Agent Memory Checkpoint & Resume (worker failure & recovery)"
    echo "  3) Fast Engine Test Suite (inmemory, btree, common, cache, fs)"
    echo "  4) Local Protocol Smoke Test (MCP stdio & A2A agent card probe)"
    echo "  a) Run All Demos and Tests sequentially"
    echo "  q) Quit"
    echo ""
    read -r -p "Enter selection [1-4, a, q]: " choice
    case "${choice}" in
      1)
        run_barrier_demo
        ;;
      2)
        run_agent_memory_demo
        ;;
      3)
        run_test_suite
        ;;
      4)
        run_protocol_smoke_test
        ;;
      a|A)
        run_all
        ;;
      q|Q)
        echo "Exiting."
        exit 0
        ;;
      *)
        echo -e "${RED}Invalid selection. Please choose 1, 2, 3, 4, a, or q.${NC}"
        ;;
    esac
  done
}

check_prerequisites

if [[ $# -eq 0 ]]; then
  interactive_menu
else
  case "$1" in
    -1|--barrier)
      run_barrier_demo
      ;;
    -2|--memory)
      run_agent_memory_demo
      ;;
    -3|--test)
      run_test_suite
      ;;
    -4|--protocol|--probe)
      run_protocol_smoke_test
      ;;
    -a|--all)
      run_all
      ;;
    -h|--help)
      show_usage
      ;;
    *)
      echo -e "${RED}Unknown option: $1${NC}"
      show_usage
      exit 1
      ;;
  esac
fi
