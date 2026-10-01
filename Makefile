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

cover:
	$(GOTEST) -coverprofile=$(COVERPROFILE) ./...
	$(GOCMD) tool cover -func=$(COVERPROFILE) | tail -1

# Rewrite the golden files in pkg/cli/testdata from what the runner actually
# prints. Read the diff before committing it: that is the whole point of them.
golden:
	$(GOTEST) ./pkg/cli -run TestGolden -update

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

.PHONY: all build test test-race cover golden vet fmt fmt-check lint lint-strict clean
