.PHONY: all build deps dev generate help test version

CHECK_FILES ?= $$(go list ./... | grep -v /vendor/)

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {sub("\\\\n",sprintf("\n%22c"," "), $$2);printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: deps generate build test ## Run all steps

build: ## Build the CLI
	@mkdir -p .gobincache
	go build -o .gobincache/goproject .

dev: ## Run the CLI
	go run .

deps: ## Download dependencies.
	go mod tidy

generate: ## Run code generation
	go generate ./...

test: ## Run tests
	go test -v $(CHECK_FILES)

version: ## Run goversion. Usage: make version bump="patch" [files="-file=README.md"]
	@if [ -z "$(bump)" ]; then \
		echo "Error: You must provide a bump argument, e.g. bump=\"patch\"" >&2; \
		exit 1; \
	fi
	go tool github.com/bcomnes/goversion/v2 $(files) $(bump)
