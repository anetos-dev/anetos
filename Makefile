# SPDX-License-Identifier: Apache-2.0

GO       ?= go
PKGS     ?= ./...
COVEROUT ?= coverage.out

.PHONY: all check fmt fmt-check vet lint test test-short cover bench vuln spdx tidy help

all: check ## Run every check CI runs

check: fmt-check spdx vet lint test ## fmt, SPDX headers, vet, lint, race tests

fmt: ## Format all Go code
	gofmt -s -w .

fmt-check: ## Fail if any file needs gofmt
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet $(PKGS)

lint: ## golangci-lint (install: https://golangci-lint.run)
	golangci-lint run $(PKGS)

test: ## Tests with the race detector
	$(GO) test -race -count=1 $(PKGS)

test-short: ## Fast tests, no race detector
	$(GO) test -short -count=1 $(PKGS)

cover: ## Coverage report (coverage.out + summary)
	$(GO) test -race -count=1 -coverprofile=$(COVEROUT) $(PKGS)
	$(GO) tool cover -func=$(COVEROUT) | tail -n 1

bench: ## Benchmarks
	$(GO) test -run='^$$' -bench=. -benchmem $(PKGS)

vuln: ## govulncheck (install: go install golang.org/x/vuln/cmd/govulncheck@latest)
	govulncheck $(PKGS)

spdx: ## Check SPDX license headers
	@./scripts/check-spdx.sh

tidy: ## go mod tidy
	$(GO) mod tidy

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
