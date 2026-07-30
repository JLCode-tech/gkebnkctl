#!/bin/bash
# Local CI gate set - mirrors .github/workflows/ci.yml jobs in order.
# Runs all unit + offline smoke-test gates.
# This is the required pre-push gate when GitHub Actions budget is unavailable.
set -euo pipefail

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

passed_gates=0
failed_gates=0

echo "=========================================="
echo "Running local CI gate set..."
echo "=========================================="
echo

# 1. go build ./...
echo "-> go build ./..."
if go build ./...; then
	printf "%b[PASS]%b go build ./...\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[FAIL]%b go build ./...\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 2. go vet ./...
echo "-> go vet ./..."
if go vet ./...; then
	printf "%b[PASS]%b go vet ./...\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[FAIL]%b go vet ./...\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 3. gofmt -l . (fail if non-empty)
echo "-> gofmt -l . (must print nothing)"
gofmt_output=$(gofmt -l .)
if [ -z "$gofmt_output" ]; then
	printf "%b[PASS]%b gofmt -l .\n" "$GREEN" "$NC"
	((passed_gates++))
else
	echo "Files not gofmt-clean:"
	echo "$gofmt_output"
	printf "%b[FAIL]%b gofmt -l .\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 4. go tool staticcheck ./...
echo "-> go tool staticcheck ./..."
if go tool staticcheck ./...; then
	printf "%b[PASS]%b go tool staticcheck ./...\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[FAIL]%b go tool staticcheck ./...\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 5. go test -race ./...
echo "-> go test -race ./..."
if go test -race ./...; then
	printf "%b[PASS]%b go test -race ./...\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[FAIL]%b go test -race ./...\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 6-8. Validate + dry-run offline gates (GKEBNKCTL_SKIP_AUTH=1)
echo "-> Building gkebnkctl binary for offline tests..."
mkdir -p bin
if go build -o bin/gkebnkctl ./cmd/gkebnkctl; then
	printf "%b[PASS]%b go build -o bin/gkebnkctl ./cmd/gkebnkctl\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[FAIL]%b go build -o bin/gkebnkctl ./cmd/gkebnkctl\n" "$RED" "$NC"
	((failed_gates++))
	exit 1
fi
echo

# 6. gkebnkctl validate
echo "-> GKEBNKCTL_SKIP_AUTH=1 gkebnkctl validate examples/host-device/cluster.yaml"
if GKEBNKCTL_SKIP_AUTH=1 ./bin/gkebnkctl validate examples/host-device/cluster.yaml; then
	printf "%b[PASS]%b gkebnkctl validate\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[FAIL]%b gkebnkctl validate\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 7. gkebnkctl up --dry-run (check for phase 50, phase 51, and "dry-run complete")
echo "-> GKEBNKCTL_SKIP_AUTH=1 gkebnkctl up --dry-run"
mkdir -p artifacts
if GKEBNKCTL_SKIP_AUTH=1 ./bin/gkebnkctl up -f examples/host-device/cluster.yaml --dry-run 2>&1 | tee artifacts/up-dryrun.log; then
	if grep -q '\[phase 50\]' artifacts/up-dryrun.log && grep -q '\[phase 51\]' artifacts/up-dryrun.log && grep -q 'dry-run complete' artifacts/up-dryrun.log; then
		printf "%b[PASS]%b gkebnkctl up --dry-run (phase 50 + phase 51 + dry-run complete)\n" "$GREEN" "$NC"
		((passed_gates++))
	else
		echo "ERROR: output missing required strings:"
		! grep -q '\[phase 50\]' artifacts/up-dryrun.log && echo "  - Missing '[phase 50]'"
		! grep -q '\[phase 51\]' artifacts/up-dryrun.log && echo "  - Missing '[phase 51]'"
		! grep -q 'dry-run complete' artifacts/up-dryrun.log && echo "  - Missing 'dry-run complete'"
		printf "%b[FAIL]%b gkebnkctl up --dry-run assertions\n" "$RED" "$NC"
		((failed_gates++))
	fi
else
	printf "%b[FAIL]%b gkebnkctl up --dry-run (exit code)\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# 8. gkebnkctl down --dry-run (check for "dry-run complete" and "NEVER deleted by teardown")
echo "-> GKEBNKCTL_SKIP_AUTH=1 gkebnkctl down --dry-run"
if GKEBNKCTL_SKIP_AUTH=1 ./bin/gkebnkctl down -f examples/host-device/cluster.yaml --dry-run 2>&1 | tee artifacts/down-dryrun.log; then
	if grep -q 'dry-run complete' artifacts/down-dryrun.log && grep -q 'NEVER deleted by teardown' artifacts/down-dryrun.log; then
		printf "%b[PASS]%b gkebnkctl down --dry-run (dry-run complete + NEVER deleted)\n" "$GREEN" "$NC"
		((passed_gates++))
	else
		echo "ERROR: output missing required strings:"
		! grep -q 'dry-run complete' artifacts/down-dryrun.log && echo "  - Missing 'dry-run complete'"
		! grep -q 'NEVER deleted by teardown' artifacts/down-dryrun.log && echo "  - Missing 'NEVER deleted by teardown'"
		printf "%b[FAIL]%b gkebnkctl down --dry-run assertions\n" "$RED" "$NC"
		((failed_gates++))
	fi
else
	printf "%b[FAIL]%b gkebnkctl down --dry-run (exit code)\n" "$RED" "$NC"
	((failed_gates++))
fi
echo

# Security gates (advisory: pass on both success and findings, warn on findings)
echo "-> Security gates (gosec, govulncheck, gitleaks):"
echo "-> go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 ./... (advisory)"
if go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 ./...; then
	printf "%b[PASS]%b gosec\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[WARN]%b gosec found findings (advisory gate - see output above)\n" "$YELLOW" "$NC"
	((passed_gates++))
fi
echo "-> go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./... (advisory)"
if go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...; then
	printf "%b[PASS]%b govulncheck\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[WARN]%b govulncheck found findings (advisory gate - see output above)\n" "$YELLOW" "$NC"
	((passed_gates++))
fi
echo "-> go run github.com/zricethezav/gitleaks/v8@v8.18.2 detect --source . --no-banner (advisory)"
echo "   Note: local run scans the worktree only; CI covers full history via fetch-depth:0"
if go run github.com/zricethezav/gitleaks/v8@v8.18.2 detect --source . --no-banner; then
	printf "%b[PASS]%b gitleaks\n" "$GREEN" "$NC"
	((passed_gates++))
else
	printf "%b[WARN]%b gitleaks found findings (advisory gate - see output above)\n" "$YELLOW" "$NC"
	((passed_gates++))
fi
echo

# Summary
echo "=========================================="
echo "CI Gate Summary"
echo "=========================================="
printf "Passed: %b%d%b\n" "$GREEN" "$passed_gates" "$NC"
printf "Failed: %b%d%b\n" "$RED" "$failed_gates" "$NC"
echo

if [ "$failed_gates" -eq 0 ]; then
	printf "%b[PASS] All gates PASSED%b\n" "$GREEN" "$NC"
	exit 0
else
	printf "%b[FAIL] Some gates FAILED%b\n" "$RED" "$NC"
	exit 1
fi
