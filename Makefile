.PHONY: security-scan lint-sec sast sca secrets-scan iac-scan

# Mirrors the checks run in .github/workflows/security.yml and codeql.yml
# so issues surface locally before a push, not after CI runs.

lint-sec:
	@echo "== gofmt =="
	@unformatted=$$(gofmt -l . | grep -v vendor || true); \
	if [ -n "$$unformatted" ]; then \
		echo "Files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "== go vet =="
	@go vet $$(go list ./... 2>/dev/null | grep -Ev '/demo$$|/demo-agents$$')

GOVULNCHECK := $(shell go env GOPATH)/bin/govulncheck

# Snapshots tracked + non-ignored files into a temp dir before handing it to
# trivy, rather than scanning the live working tree directly. Local runtime
# state that's gitignored (dev server session data under
# tools/httpserver/sessions/, .sop_data/, etc.) never ends up in a git
# checkout, and scanning it locally produces alarming noise about "leaked"
# tokens that were never committed anywhere.
snapshot-tracked = tmpdir=$$(mktemp -d); \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	git ls-files -z --cached --others --exclude-standard | tar -c --null -T - | tar -x -C "$$tmpdir";

sca:
	@echo "== govulncheck =="
	@test -x "$(GOVULNCHECK)" || go install golang.org/x/vuln/cmd/govulncheck@latest
	@$(GOVULNCHECK) $$(go list ./... 2>/dev/null | grep -Ev '/demo$$|/demo-agents$$')
	@GOOS=js GOARCH=wasm $(GOVULNCHECK) ./demo/...
	@GOOS=js GOARCH=wasm $(GOVULNCHECK) ./demo-agents/...
	@if command -v trivy >/dev/null 2>&1; then \
		echo "== trivy fs (critical/high block, medium warn) =="; \
		$(snapshot-tracked) \
		trivy fs --offline-scan --severity CRITICAL,HIGH --exit-code 1 --ignore-unfixed "$$tmpdir" && \
		trivy fs --offline-scan --severity MEDIUM --exit-code 0 "$$tmpdir"; \
	else \
		echo "trivy not installed locally, skipping fs scan (see .github/workflows/security.yml for CI coverage): https://trivy.dev/latest/getting-started/installation/"; \
	fi

secrets-scan:
	@if command -v gitleaks >/dev/null 2>&1; then \
		gitleaks detect --source . --config .gitleaks.toml --redact ; \
	else \
		echo "gitleaks not installed locally: https://github.com/gitleaks/gitleaks#installing"; \
		exit 1; \
	fi

iac-scan:
	@if command -v trivy >/dev/null 2>&1; then \
		echo "== trivy config (critical/high block, medium warn) =="; \
		$(snapshot-tracked) \
		trivy config --severity CRITICAL,HIGH --exit-code 1 "$$tmpdir" && \
		trivy config --severity MEDIUM --exit-code 0 "$$tmpdir"; \
	else \
		echo "trivy not installed locally: https://trivy.dev/latest/getting-started/installation/"; \
		exit 1; \
	fi

sast:
	@echo "SAST (CodeQL) runs in CI on push/PR; see .github/workflows/codeql.yml."
	@echo "For a local approximation, run 'make lint-sec' plus 'go vet' above."

security-scan: lint-sec sca secrets-scan iac-scan
	@echo "All local security checks passed."
