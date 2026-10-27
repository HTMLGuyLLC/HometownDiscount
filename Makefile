# Hometown Discount: common tasks.
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint install clean

build: ## Build ./bin/htd
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/htd ./cmd/htd

test: ## Run every test (sandbox canaries included)
	go test ./...

lint: ## Formatting and vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && false)
	go vet ./...

install: build ## Install htd to /usr/local/bin
	install -m 0755 bin/htd /usr/local/bin/htd

clean:
	rm -rf bin dist
