BINARY  := repo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local

.PHONY: all build install uninstall test lint fmt tidy clean completions

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

clean:
	rm -rf bin completions
