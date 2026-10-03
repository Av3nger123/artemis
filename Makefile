# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOCLEAN=$(GOCMD) clean

# Binary name
BINARY_NAME=artemis

# Main package path
MAIN_PATH=./cmd/cli

# Every package in the module. The old SRC_DIRS/$(wildcard ./*.go) pair expanded
# to nothing -- all the code lives under cmd/ and pkg/ -- so lint ran on an empty
# argument list.
PKGS=./...

COVERPROFILE=coverage.out

all: vet lint test build

build:
	$(GOBUILD) -o $(BINARY_NAME) $(MAIN_PATH)

test:
	$(GOTEST) ./...

# -race catches what a single-threaded run never will; this is what CI runs.
test-race:
	$(GOTEST) -race ./...

# The tests that need a real browser: pkg/steps/browserstep's driver over
# Chromium, and pkg/cli's whole run against the local fixture server.
#
# Not part of `test`, and not run in CI. CI has no browser, and the first run on
# a cold machine downloads about 683 MB of Chromium -- see docs/browser-engine.md.
# Everything below the browser is covered by the default suite against a fake
# driver, which is what keeps CI honest about this code.
test-browser:
	$(GOTEST) -tags browser ./pkg/session ./pkg/steps/browserstep/... ./pkg/cli

# The flake check: ten consecutive browser runs. A browser suite that passes
# once and fails one run in five is worse than no suite, so "not flaky" is a
# thing to measure rather than hope for.
#
# No -run filter: the browser-tagged tests in pkg/steps/browserstep are not all
# named "Browser", and a flake check that silently matched none of them would be
# the most expensive way to prove nothing.
test-browser-repeat:
	$(GOTEST) -tags browser -count=10 ./pkg/session ./pkg/steps/browserstep/... ./pkg/cli

cover:
	$(GOTEST) -coverprofile=$(COVERPROFILE) ./...
	$(GOCMD) tool cover -func=$(COVERPROFILE) | tail -1

# Rewrite the golden files from what the code actually prints -- the run reports
# in pkg/cli/testdata/art, the rendered diagnostics in pkg/dsl/diag/testdata, the
# invalid-file corpus in pkg/dsl/testdata/invalid, the canonical printer's
# output in pkg/dsl/print/testdata/canon, and the tree encoding and its schema
# in pkg/dsl/encode/testdata, and the exported pytest modules in
# pkg/codegen/testdata.
# Read the diff before committing it: that is the whole point of them.
#
# The corpus's own -update leaves the fixtures waiting on a later stage alone --
# their goldens are written by hand, ahead of the stage that will produce them.
golden:
	$(GOTEST) ./pkg/cli -run TestGolden -update
	$(GOTEST) ./pkg/dsl/diag -update
	$(GOTEST) ./pkg/dsl -update
	$(GOTEST) ./pkg/dsl/print -update
	$(GOTEST) ./pkg/dsl/encode -update
	$(GOTEST) ./pkg/codegen -update

vet:
	$(GOVET) $(PKGS)

fmt:
	$(GOCMD) fmt $(PKGS)

# Reports badly formatted files without rewriting them, for CI.
fmt-check:
	@unformatted=$$(gofmt -l . 2>/dev/null); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi

# A missing linter must not stop anyone from building the binary, so this skips
# with a note instead of failing. CI installs a pinned golangci-lint, so lint is
# still a hard gate there -- see .github/workflows/ci.yml.
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run $(PKGS); \
	else \
		echo "golangci-lint not found; skipping lint."; \
		echo "install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8"; \
	fi

# Fails when golangci-lint is missing, for anyone who wants the gate locally.
lint-strict:
	golangci-lint run $(PKGS)

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME) $(COVERPROFILE)

.PHONY: all build test test-race test-browser test-browser-repeat cover golden vet fmt fmt-check lint lint-strict clean
