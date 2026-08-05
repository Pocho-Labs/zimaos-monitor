BINARY  := zimaos-monitor
CMD     := ./cmd/zimaos-monitor
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: all build build-linux run-dry test test-install tidy clean

all: build

# Build for the current platform (useful for local testing)
build:
	go build -ldflags="-X main.version=$(VERSION)" -o $(BIN_DIR)/$(BINARY) $(CMD)

# Cross-compile for ZimaOS (Linux x86_64)
build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(CMD)

# Run locally in dry-run mode (no MQTT, prints JSON to stdout)
run-dry:
	go run $(CMD) --dry-run

test:
	go test ./...
	$(MAKE) test-install

test-install:
	sh -n scripts/install.sh
	python3 scripts/install_test.py

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)
