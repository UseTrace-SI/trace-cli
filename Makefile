.PHONY: build companions test test-companions lint test-structure test-sync test-sync-integration e2e perf
build:
	CGO_ENABLED=0 go build -o bin/trace ./cmd/trace
companions: build
	CGO_ENABLED=0 go -C ../trace-brain build -o ../trace-cli/bin/trace-brain ./cmd/trace-brain
	CGO_ENABLED=0 go -C ../trace-graph build -o ../trace-cli/bin/trace-graph ./cmd/trace-graph
test-companions:
	python3 scripts/companions_e2e.py
test:
	# Avoid other packages' builds/tests competing with the hook latency checks.
	go test -race -p 1 ./...
test-structure:
	CGO_ENABLED=1 go test -race -tags treesitter ./internal/structure ./internal/engine
test-sync:
	go test -count=1 -race ./internal/syncclient
test-sync-integration:
	test -d ../trace-platform
	go test -count=1 -race -tags syncintegration ./internal/syncclient
	npm --prefix ../trace-platform run check
lint:
	go vet ./...
	staticcheck ./...
e2e:
	./scripts/e2e.sh
perf:
	./scripts/perf.sh
