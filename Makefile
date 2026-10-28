.PHONY: build test lint test-structure test-sync test-sync-integration e2e perf
build:
	CGO_ENABLED=0 go build -o bin/trace ./cmd/trace
test:
	go test -race ./...
test-structure:
	CGO_ENABLED=1 go test -race -tags treesitter ./internal/structure ./internal/engine
test-sync:
	go test -count=1 -race ./internal/syncclient
test-sync-integration:
	test -d ../trace-app
	go test -count=1 -race -tags syncintegration ./internal/syncclient
	npm --prefix ../trace-app run check
lint:
	go vet ./...
	staticcheck ./...
e2e:
	./scripts/e2e.sh
perf:
	./scripts/perf.sh
