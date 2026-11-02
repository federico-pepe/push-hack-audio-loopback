BINARY    := push-audio-loopback
SRC_DIR   := src
BUILD_DIR := build

# Target: Ableton Push 3 (Intel x86_64 Linux)
GOOS      := linux
GOARCH    := amd64

# No cgo needed — plain build flags, static binary, strip debug info.
LDFLAGS   := -s -w

.PHONY: all build mixer clean fmt vet

all: build

build:
	@echo "Building $(BINARY) for $(GOOS)/$(GOARCH)..."
	@mkdir -p $(BUILD_DIR)
	cd $(SRC_DIR) && \
	  CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	  go build -ldflags "$(LDFLAGS)" \
	  -o ../$(BUILD_DIR)/$(BINARY) .
	@cp $(BUILD_DIR)/$(BINARY) $(BINARY)
	@echo "Done: $(BINARY)"

build-local:
	@echo "Building $(BINARY) for local machine..."
	@mkdir -p $(BUILD_DIR)
	cd $(SRC_DIR) && \
	  go build -ldflags "$(LDFLAGS)" \
	  -o ../$(BUILD_DIR)/$(BINARY)-local .
	@echo "Done: $(BUILD_DIR)/$(BINARY)-local"

# Prototype of the audio bus mixer (docs/audio-bus.md in ableton-push-hack).
# Needs cgo (libasound) and the audiobus package, which is not in a tagged
# core yet: build in Docker against a local core/ checkout.
CORE ?= $(HOME)/Documents/GitHub/ableton-push-hack/core
mixer:
	docker run --rm --platform linux/amd64 -v "$(CURDIR)":/work -v "$(CORE)":/core -w /work/src \
	  golang:1.25-bookworm sh -c "apt-get update -qq && apt-get install -qq -y libasound2-dev >/dev/null && \
	  cp go.mod /tmp/m.mod && cp go.sum /tmp/m.sum && \
	  go mod edit -modfile=/tmp/m.mod -replace github.com/federico-pepe/ableton-push-hack/core=/core && \
	  go mod tidy -modfile=/tmp/m.mod && \
	  CGO_ENABLED=1 go build -modfile=/tmp/m.mod -ldflags '-s -w -linkmode external -extldflags -Wl,--dynamic-linker=/lib/ld-linux-x86-64.so.2' -o ../mixer ./cmd/mixer && \
	  chmod +x ../mixer"

fmt:
	cd $(SRC_DIR) && go fmt ./...

vet:
	cd $(SRC_DIR) && go vet ./...

clean:
	rm -rf $(BUILD_DIR) $(BINARY)
