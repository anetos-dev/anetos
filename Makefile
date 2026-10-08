# SPDX-License-Identifier: Apache-2.0

GO       ?= go
COVEROUT ?= coverage.out
# Every module in the repository: the core, driver modules, and examples
# that need drivers. Targets run in each one.
MODULES  ?= $(patsubst %/go.mod,%,$(shell find . -name go.mod -not -path './.git/*' | sort))
EACH      = for m in $(MODULES); do echo "== $$m"; (cd $$m &&
DONE      = ) || exit 1; done

.PHONY: all check fmt fmt-check vet lint test test-short cover bench bench-check bench-compare vuln spdx docs-check api-docs gen-check tidy help

all: check ## Run every check CI runs

check: fmt-check spdx docs-check api-docs gen-check vet lint test bench-check ## fmt, SPDX headers, doc snippets, API doc comments, generated code, vet, lint, race tests, allocation budgets

fmt: ## Format all Go code
	gofmt -s -w .

fmt-check: ## Fail if any file needs gofmt
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## go vet
	@$(EACH) $(GO) vet ./... $(DONE)

lint: ## golangci-lint (install: https://golangci-lint.run)
	@$(EACH) golangci-lint run ./... $(DONE)

# The cli module's tests create, build and test whole projects (anetos new,
# make:auth…): with the race detector and the other packages beside them,
# they need more than go test's usual budget.
test: ## Tests with the race detector (set ANETOS_TEST_POSTGRES_URL / ANETOS_TEST_MYSQL_URL / ANETOS_TEST_REDIS_URL for those drivers)
	@$(EACH) $(GO) test -race -count=1 -timeout=10m ./... $(DONE)

test-short: ## Fast tests, no race detector
	@$(EACH) $(GO) test -short -count=1 -timeout=5m ./... $(DONE)

cover: ## Coverage summary per module (driver modules report db package coverage)
	@$(EACH) $(GO) test -count=1 -coverpkg=./...,anetos.dev/anetos/db/... -coverprofile=$(COVEROUT) ./... >/dev/null && $(GO) tool cover -func=$(COVEROUT) | tail -n 1 $(DONE)

bench: ## Benchmarks
	@$(EACH) $(GO) test -run='^$$' -bench=. -benchmem ./... $(DONE)

bench-check: ## Allocation budgets of requests and queries (bench/budget_test.go, without the race detector)
	@cd bench && $(GO) test -count=1 -run '^TestBudgets$$' .

BASE ?= main
bench-compare: ## Compare the benchmarks with BASE (default main) on this machine; fails on a regression
	@BASE=$(BASE) ./scripts/bench-compare.sh

vuln: ## govulncheck (install: go install golang.org/x/vuln/cmd/govulncheck@latest)
	@$(EACH) govulncheck ./... $(DONE)

spdx: ## Check SPDX license headers
	@./scripts/check-spdx.sh

docs-check: ## Check doc code blocks match their example regions, and the pages have their sidebar group
	@$(GO) run ./internal/cmd/docsnippets
	@$(GO) run ./internal/cmd/docnav

api-docs: ## Check every exported identifier has a doc comment
	@$(GO) run ./internal/cmd/doccheck

gen-check: ## Check generated model columns are up to date
	@for m in examples/database examples/forms examples/saas examples/tracker examples/tutorial examples/bookmarks; do (cd $$m && $(GO) tool anetos gen -check) || exit 1; done

tidy: ## go mod tidy
	@$(EACH) $(GO) mod tidy $(DONE)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
