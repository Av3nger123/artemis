# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean

# Binary name
BINARY_NAME=artemis

# Main package path
MAIN_PATH=$(PWD)/cmd/cli

# Directories
SRC_DIRS=.

all: lint build

build: lint
	$(GOBUILD) -o $(BINARY_NAME) $(MAIN_PATH)

test:
	$(GOTEST) -v ./...

lint:
	golangci-lint run $(foreach dir,$(SRC_DIRS),$(wildcard $(dir)/*.go))

clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)

.PHONY: all build test lint clean