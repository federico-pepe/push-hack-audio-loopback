BINARY    := push-audio-loopback
SRC_DIR   := src
BUILD_DIR := build

# audiobus is not in a tagged core yet: build against a local core/ checkout.
# Drop CORE and the MODFILE lines once core/v* has audiobus and src/go.mod
# pins it.
CORE      ?= $(HOME)/Documents/GitHub/ableton-push-hack/core

# Target: Ableton Push 3 (Intel x86_64 Linux). The mixer writes the card with
# ALSA, so the binary needs cgo and libasound: build it in a linux/amd64
# container, like push-braids. A plain `go build` on a Mac cannot do this.
DOCKER    := docker run --rm --platform linux/amd64 \
               -v "$(CURDIR)":/work -v "$(CORE)":/core -w /work/src \
               golang:1.25-bookworm
MODFILE   := cp go.mod /tmp/m.mod && cp go.sum /tmp/m.sum && \
               go mod edit -modfile=/tmp/m.mod -replace github.com/federico-pepe/ableton-push-hack/core=/core && \
               go mod tidy -modfile=/tmp/m.mod
APT       := apt-get update -qq && apt-get install -qq -y libasound2-dev >/dev/null

.PHONY: all build test test-local clean fmt vet

all: build

build:
	@echo "Building $(BINARY) (cgo, linux/amd64, via Docker)..."
	@mkdir -p $(BUILD_DIR)
	$(DOCKER) sh -c "$(APT) && $(MODFILE) && \
	  CGO_ENABLED=1 go build -modfile=/tmp/m.mod -ldflags '-s -w -linkmode external -extldflags -Wl,--dynamic-linker=/lib/ld-linux-x86-64.so.2' -o ../$(BUILD_DIR)/$(BINARY) . && \
	  chmod +x ../$(BUILD_DIR)/$(BINARY)"
	@cp $(BUILD_DIR)/$(BINARY) $(BINARY)
	@echo "Done: $(BINARY)"

# vet and tests, in the same container as the build (real ALSA code included).
test:
	$(DOCKER) sh -c "$(APT) && $(MODFILE) && go vet -modfile=/tmp/m.mod ./... && go test -modfile=/tmp/m.mod -race ./..."

# Portable parts only, on this machine (the ALSA code is stubbed out).
test-local:
	@mkdir -p $(BUILD_DIR)
	cp $(SRC_DIR)/go.mod $(BUILD_DIR)/m.mod && cp $(SRC_DIR)/go.sum $(BUILD_DIR)/m.sum
	cd $(SRC_DIR) && go mod edit -modfile=../$(BUILD_DIR)/m.mod -replace github.com/federico-pepe/ableton-push-hack/core=$(CORE) && \
	  go mod tidy -modfile=../$(BUILD_DIR)/m.mod && go vet -modfile=../$(BUILD_DIR)/m.mod ./... && go test -modfile=../$(BUILD_DIR)/m.mod ./...

fmt:
	cd $(SRC_DIR) && go fmt ./...

vet: test-local

clean:
	rm -rf $(BUILD_DIR) $(BINARY)
