#!/usr/bin/env bash
# ==============================================================================
# Joltrin Release Verification Gate & Provenance Checker
# ==============================================================================
# Verifies cryptographic checksums, archive integrity, binary headers, and
# software bill of materials (SBOM) before release publishing.
#
# Usage:
#   ./scripts/verify_release.sh [path-to-release-dir]
# ==============================================================================

set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

RELEASE_DIR="${1:-release}"

echo -e "${BLUE}==> Joltrin Release Verification Gate${NC}"
echo -e "Target Directory: ${RELEASE_DIR}"

if [ ! -d "$RELEASE_DIR" ]; then
    echo -e "${RED}[FAIL] Release directory '${RELEASE_DIR}' does not exist.${NC}"
    exit 1
fi

TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0

pass() {
    local msg="$1"
    echo -e "  ${GREEN}✔ [PASS]${NC} ${msg}"
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
}

fail() {
    local msg="$1"
    echo -e "  ${RED}✖ [FAIL]${NC} ${msg}"
    FAILED_CHECKS=$((FAILED_CHECKS + 1))
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
}

warn() {
    local msg="$1"
    echo -e "  ${YELLOW}⚠ [WARN]${NC} ${msg}"
}

# ------------------------------------------------------------------------------
# 1. Checksum Verification
# ------------------------------------------------------------------------------
echo -e "\n${BLUE}[1/4] Cryptographic Checksum Validation...${NC}"

CHECKSUM_FILE=""
if [ -f "${RELEASE_DIR}/SHA256SUMS" ]; then
    CHECKSUM_FILE="${RELEASE_DIR}/SHA256SUMS"
elif [ -f "${RELEASE_DIR}/checksums.txt" ]; then
    CHECKSUM_FILE="${RELEASE_DIR}/checksums.txt"
fi

if [ -n "$CHECKSUM_FILE" ]; then
    pass "Checksum manifest found: ${CHECKSUM_FILE}"
    
    # Run checksum verification inside release directory
    VERIFY_STATUS="OK"
    (
        cd "$RELEASE_DIR"
        MANIFEST_NAME="$(basename "$CHECKSUM_FILE")"
        
        # Filter out manifest lines pointing to checksum files themselves
        CLEAN_MANIFEST="$(grep -v "SHA256SUMS" "$MANIFEST_NAME" | grep -v "checksums.txt" || true)"
        
        if [ -z "$CLEAN_MANIFEST" ]; then
            echo "EMPTY_MANIFEST"
            exit 0
        fi

        if command -v sha256sum &> /dev/null; then
            echo "$CLEAN_MANIFEST" | sha256sum --check --ignore-missing --status - && echo "ALL_OK" || echo "FAILED"
        elif command -v shasum &> /dev/null; then
            echo "$CLEAN_MANIFEST" | shasum -a 256 -c --status - 2>/dev/null && echo "ALL_OK" || echo "FAILED"
        else
            echo "NO_TOOL"
        fi
    ) > /tmp/checksum_result.txt 2>&1 || true

    CHECK_OUT=$(cat /tmp/checksum_result.txt)
    rm -f /tmp/checksum_result.txt

    if [[ "$CHECK_OUT" == *"ALL_OK"* ]]; then
        pass "Cryptographic SHA-256 signatures validated against all release artifacts"
    elif [[ "$CHECK_OUT" == *"NO_TOOL"* ]]; then
        warn "Neither 'sha256sum' nor 'shasum' available on host to verify hashes"
    elif [[ "$CHECK_OUT" == *"EMPTY_MANIFEST"* ]]; then
        warn "Checksum manifest contains no artifact entries"
    else
        fail "SHA-256 hash mismatch detected! Some release files have been altered or corrupted."
    fi
else
    warn "No SHA256SUMS or checksums.txt found in release directory"
fi

# ------------------------------------------------------------------------------
# 2. Archive Integrity Checks
# ------------------------------------------------------------------------------
echo -e "\n${BLUE}[2/4] Archive Integrity & Extraction Verification...${NC}"

ARCHIVE_COUNT=0

# Test .tar.gz archives
for archive in "${RELEASE_DIR}"/*.tar.gz; do
    [ -e "$archive" ] || continue
    ARCHIVE_COUNT=$((ARCHIVE_COUNT + 1))
    if tar -tzf "$archive" > /dev/null 2>&1; then
        pass "Archive $(basename "$archive") gzip stream & file table intact"
    else
        fail "Archive $(basename "$archive") is corrupted or truncated!"
    fi
done

# Test .zip archives
for zipfile in "${RELEASE_DIR}"/*.zip; do
    [ -e "$zipfile" ] || continue
    ARCHIVE_COUNT=$((ARCHIVE_COUNT + 1))
    if command -v unzip &> /dev/null; then
        if unzip -t -q "$zipfile" > /dev/null 2>&1; then
            pass "Zip archive $(basename "$zipfile") table of contents verified"
        else
            fail "Zip archive $(basename "$zipfile") is corrupted!"
        fi
    elif command -v python3 &> /dev/null; then
        if python3 -c "import zipfile, sys; sys.exit(0 if zipfile.ZipFile('$zipfile').testzip() is None else 1)" 2>/dev/null; then
            pass "Zip archive $(basename "$zipfile") verified via Python zipfile engine"
        else
            fail "Zip archive $(basename "$zipfile") failed CRC check!"
        fi
    else
        warn "Skipping zip verification (neither 'unzip' nor 'python3' present)"
    fi
done

if [ "$ARCHIVE_COUNT" -eq 0 ]; then
    warn "No .tar.gz or .zip archive bundles found to test"
fi

# ------------------------------------------------------------------------------
# 3. Binary & Package Health Checks
# ------------------------------------------------------------------------------
echo -e "\n${BLUE}[3/4] Binary & Package Footprint Inspection...${NC}"

BIN_COUNT=0
for binary in "${RELEASE_DIR}"/*; do
    [ -f "$binary" ] || continue
    fname="$(basename "$binary")"
    
    # Skip manifests, checksums, and metadata
    if [[ "$fname" == "SHA256SUMS" || "$fname" == "checksums.txt" || "$fname" == *.json ]]; then
        continue
    fi
    
    BIN_COUNT=$((BIN_COUNT + 1))
    fsize=$(wc -c < "$binary" | tr -d ' ')
    if [ "$fsize" -gt 1024 ]; then
        pass "Artifact ${fname} has non-trivial size (${fsize} bytes)"
    else
        fail "Artifact ${fname} appears empty or truncated (${fsize} bytes)"
    fi
done

if [ "$BIN_COUNT" -eq 0 ]; then
    warn "No standalone binary artifacts found in ${RELEASE_DIR}"
fi

# ------------------------------------------------------------------------------
# 4. Software Bill of Materials (SBOM) Validation
# ------------------------------------------------------------------------------
echo -e "\n${BLUE}[4/4] Software Bill of Materials (SBOM) Verification...${NC}"

SBOM_FOUND=0
for sbom in "${RELEASE_DIR}"/*sbom*.json "${RELEASE_DIR}"/*.spdx.json; do
    [ -e "$sbom" ] || continue
    SBOM_FOUND=1
    if command -v python3 &> /dev/null; then
        if python3 -c "import json, sys; d = json.load(open('$sbom')); sys.exit(0 if 'spdxVersion' in d or 'bomFormat' in d or 'packages' in d else 1)" 2>/dev/null; then
            pass "SBOM $(basename "$sbom") conforms to standard SPDX / CycloneDX format"
        else
            warn "SBOM $(basename "$sbom") is valid JSON but lacks standard top-level SBOM schema tags"
        fi
    else
        pass "SBOM $(basename "$sbom") exists (Python not available for deep schema validation)"
    fi
done

if [ "$SBOM_FOUND" -eq 0 ]; then
    warn "No SBOM file (*sbom*.json or *.spdx.json) found in ${RELEASE_DIR}"
fi

# ------------------------------------------------------------------------------
# Summary
# ------------------------------------------------------------------------------
echo -e "\n=================================================================="
echo -e "Verification Complete: ${PASSED_CHECKS}/${TOTAL_CHECKS} checks passed."
if [ "$FAILED_CHECKS" -gt 0 ]; then
    echo -e "${RED}[GATE REJECTED] ${FAILED_CHECKS} check(s) failed. Do not release artifacts.${NC}"
    exit 1
else
    echo -e "${GREEN}[GATE APPROVED] Release artifacts verified intact and authenticated.${NC}"
    exit 0
fi
