BINARY    := push-audio-loopback
SRC_DIR   := src
BUILD_DIR := build

# Target: Ableton Push 3 (Intel x86_64 Linux). The mixer writes the card with
# ALSA, so the binary needs cgo and libasound: build it in a linux/amd64
# container, like push-braids. A plain `go build` on a Mac cannot do this.
DOCKER    := docker run --rm --platform linux/amd64 \
               -v "$(CURDIR)":/work -w /work/src \
               golang:1.25-bookworm
APT       := apt-get update -qq && apt-get install -qq -y libasound2-dev >/dev/null

.PHONY: all build test test-local clean fmt vet

all: build

build:
	@echo "Building $(BINARY) (cgo, linux/amd64, via Docker)..."
	@mkdir -p $(BUILD_DIR)
	$(DOCKER) sh -c "$(APT) && \
	  CGO_ENABLED=1 go build -ldflags '-s -w -linkmode external -extldflags -Wl,--dynamic-linker=/lib/ld-linux-x86-64.so.2' -o ../$(BUILD_DIR)/$(BINARY) . && \
	  chmod +x ../$(BUILD_DIR)/$(BINARY) && \
	  ! objdump -T ../$(BUILD_DIR)/$(BINARY) | grep -E 'GLIBC_2\.(3[6-9]|[4-9][0-9])' || \
	  { echo 'ERROR: binary needs a glibc newer than 2.35 (Push 3 has 2.35)'; exit 1; }"
	@cp $(BUILD_DIR)/$(BINARY) $(BINARY)
	@echo "Done: $(BINARY)"

# vet and tests, in the same container as the build (real ALSA code included).
test:
	$(DOCKER) sh -c "$(APT) && go vet ./... && go test -race ./..."

# Portable parts only, on this machine (the ALSA code is stubbed out).
test-local:
	cd $(SRC_DIR) && go vet ./... && go test ./...

fmt:
	cd $(SRC_DIR) && go fmt ./...

vet: test-local

clean:
	rm -rf $(BUILD_DIR) $(BINARY)
