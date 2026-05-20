GO        ?= go
IMAGE     ?= cert-check-service:local
LISTEN    ?= :8080
FUZZTIME  ?= 30s

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the server binary into bin/server.
	$(GO) build -o bin/server ./cmd/server

.PHONY: run
run: ## Run the server natively on $(LISTEN).
	LISTEN_ADDR=$(LISTEN) $(GO) run ./cmd/server

.PHONY: test
test: ## Run unit + property + contract tests with the race detector.
	$(GO) test -race -timeout=2m ./...

.PHONY: cover
cover: ## Run tests with coverage profile (writes coverage.out).
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: bench
bench: ## Run benchmarks (microbench; not the full load test).
	$(GO) test -bench=. -benchmem -benchtime=2s -run='^$$' ./...

.PHONY: fuzz-serial
fuzz-serial: ## Fuzz parseSerial for $(FUZZTIME).
	$(GO) test -fuzz=FuzzParseSerial -fuzztime=$(FUZZTIME) ./internal/handler/...

.PHONY: fuzz-at
fuzz-at: ## Fuzz parseAt for $(FUZZTIME).
	$(GO) test -fuzz=FuzzParseAt -fuzztime=$(FUZZTIME) ./internal/handler/...

.PHONY: lint
lint: ## Run golangci-lint.
	golangci-lint run --timeout=2m

.PHONY: fmt
fmt: ## Apply gofmt + goimports.
	gofmt -w .

.PHONY: docker
docker: ## Build the Docker image.
	docker build -t $(IMAGE) .

.PHONY: load-test
load-test: ## Run vegeta against a running server on $(LISTEN).
	@command -v vegeta >/dev/null || { echo "install vegeta: brew install vegeta"; exit 1; }
	vegeta attack -rate=10000 -duration=30s -targets=loadtest/targets.txt | vegeta report

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/ coverage.out
