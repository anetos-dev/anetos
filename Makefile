# SPDX-License-Identifier: Apache-2.0

GO       ?= go
COVEROUT ?= coverage.out
# Every module in the repository: the core, driver modules, and examples
# that need drivers. Targets run in each one. A frontend's go.mod only
# keeps the go command out of its node_modules: it has no Go.
MODULES  ?= $(patsubst %/go.mod,%,$(shell find . -name go.mod -not -path './.git/*' -not -path '*/node_modules/*' -not -path './examples/*/frontend/*' | sort))
EACH      = for m in $(MODULES); do echo "== $$m"; (cd $$m &&
DONE      = ) || exit 1; done

.PHONY: all check fmt fmt-check vet lint test test-short cover bench bench-check bench-compare vuln spdx docs-check api-docs api-check api-update gen-check tidy release release-rehearsal help

all: check ## Run every check CI runs

check: fmt-check spdx docs-check api-docs api-check gen-check vet lint test bench-check ## fmt, SPDX headers, doc snippets, API doc comments, the API files, generated code, vet, lint, race tests, allocation budgets

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
	@$(EACH) $(GO) test -race -count=1 -timeout=15m ./... $(DONE)

test-short: ## Fast tests, no race detector
	@$(EACH) $(GO) test -short -count=1 -timeout=5m ./... $(DONE)

# The modules a release publishes (docs/contributing/releasing.md).
PUBLISHED = . admin cli $(patsubst %/go.mod,%,$(sort $(wildcard drivers/*/go.mod plugins/*/go.mod)))

# Each published module's tests count every module's code they run, so the
# drivers' tests count the core's db packages; covermerge merges them
# (design D332). The database drivers' tests need ANETOS_TEST_*_URL, as for
# make test, or they skip and their code counts as not covered. The merged
# profile's paths are relative to the repository, which go tool cover
# doesn't read: use a module's own profile from inside it
# (cd admin && go tool cover -func=../.cover/admin.out).
cover: ## Coverage of the published modules, merged into coverage.out (CI sends it to Codecov; paths relative to the repository); COVER_VERBOSE=1 lists each package
	@rm -rf .cover && mkdir .cover
	@for m in $(PUBLISHED); do echo "== $$m"; n=$$(echo "$$m" | tr ./ __); \
		(cd $$m && $(GO) test -count=1 -timeout=45m -coverpkg=./...,anetos.dev/anetos/... -coverprofile=$(CURDIR)/.cover/$$n.out ./... >$(CURDIR)/.cover/$$n.log 2>&1) \
		|| { grep -vE '^(ok|\?) ' .cover/$$n.log | tail -n 200; exit 1; }; done
	@$(GO) run ./internal/cmd/covermerge -o $(COVEROUT) $(if $(COVER_VERBOSE),-v) .cover/*.out

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

docs-check: ## Check doc code blocks match their example regions, and the pages have their sidebar group and their images
	@$(GO) run ./internal/cmd/docsnippets
	@$(GO) run ./internal/cmd/docnav

api-docs: ## Check every exported identifier has a doc comment
	@$(GO) run ./internal/cmd/doccheck

api-check: ## Check api/*.txt list the exported API of each module (make api-update after an intended change)
	@$(GO) run ./internal/cmd/apisnap -check

api-update: ## Write the exported API of each module to api/*.txt
	@$(GO) run ./internal/cmd/apisnap

gen-check: ## Check generated model columns (and examples/spa's TypeScript types) are up to date
	@# The types command runs the app's setup, which needs settings: the
	@# examples have no .env.
	@for m in examples/database examples/forms examples/saas examples/tracker examples/tutorial examples/bookmarks examples/spa; do (cd $$m && APP_ENV=development APP_KEY=base64:$$(head -c32 /dev/urandom | base64) $(GO) tool anetos generate --check) || exit 1; done

tidy: ## go mod tidy
	@$(EACH) $(GO) mod tidy $(DONE)

LOCALES ?= ../locales

release: ## Prepare release VERSION (vX.Y.Z): CHANGELOGs checked, requirements set here and in LOCALES (LOCALES= for none), tags printed
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=vX.Y.Z [LOCALES=../locales]"; exit 2; }
	@if [ -n "$(LOCALES)" ] && [ ! -f "$(abspath $(LOCALES))/go.mod" ]; then echo "make release: no go.mod in LOCALES=$(LOCALES) (LOCALES= to release without it)"; exit 2; fi
	@cd cli && $(GO) run ./internal/cmd/release prepare -root .. $(if $(LOCALES),-locales "$(abspath $(LOCALES))") $(VERSION)

release-rehearsal: ## Install the committed tree from a module proxy on disk, as a release; anetos new, build and test (web and api)
	@cd cli && $(GO) run ./internal/cmd/release rehearse -root ..

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
