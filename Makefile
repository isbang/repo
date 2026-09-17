BINARY  := repo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local

# Platforms `make dist` cross-compiles release archives for.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all build install uninstall test lint fmt tidy clean completions dist

all: build

build: ## Build ./bin/repo
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

install: ## Install to $PREFIX/bin (default ~/.local/bin)
	install -d $(PREFIX)/bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(PREFIX)/bin/$(BINARY) .
	@echo "installed $(PREFIX)/bin/$(BINARY) ($(VERSION))"

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)

test:
	go test ./...

lint:
	go vet ./...
	gofmt -l .

fmt:
	gofmt -w .

tidy:
	go mod tidy

completions: build ## Write shell completions into ./completions
	mkdir -p completions
	./bin/$(BINARY) completion bash > completions/$(BINARY).bash
	./bin/$(BINARY) completion zsh > completions/_$(BINARY)
	./bin/$(BINARY) completion fish > completions/$(BINARY).fish

dist: ## Cross-compile release archives and checksums into ./dist
	rm -rf dist
	mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		bin=$(BINARY); if [ "$$os" = windows ]; then bin=$(BINARY).exe; fi; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$$bin . || exit 1; \
		tar -czf "dist/$(BINARY)_$(VERSION)_$${os}_$${arch}.tar.gz" -C dist "$$bin" -C $(CURDIR) LICENSE README.md || exit 1; \
		rm -f dist/$$bin; \
	done
	cd dist && { sha256sum *.tar.gz > checksums.txt 2>/dev/null || shasum -a 256 *.tar.gz > checksums.txt; }
	@echo "dist/ ($(VERSION))" && ls dist

clean:
	rm -rf bin completions dist
