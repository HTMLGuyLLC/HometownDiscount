# Hometown Discount: common tasks.
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.PHONY: build test lint install dist clean

build: ## Build ./bin/htd
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/htd ./cmd/htd

test: ## Run every test (sandbox canaries included)
	go test ./...

lint: ## Formatting and vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && false)
	go vet ./...

install: build ## Install htd to /usr/local/bin
	install -m 0755 bin/htd /usr/local/bin/htd

# Release archives: dist/htd_<os>_<arch>.tar.gz plus SHA256SUMS. Each archive
# carries the LICENSE and README, since the license notice must go with every copy.
dist: ## Build release archives for every platform into dist/
	rm -rf dist && mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; d=dist/htd_$${os}_$${arch}; \
		mkdir -p $$d; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$d/htd ./cmd/htd; \
		cp LICENSE README.md $$d/; \
		COPYFILE_DISABLE=1 tar -czf $$d.tar.gz -C $$d htd LICENSE README.md; \
		rm -rf $$d; \
		echo "built $$d.tar.gz"; \
	done
	cd dist && shasum -a 256 *.tar.gz > SHA256SUMS

clean:
	rm -rf bin dist
