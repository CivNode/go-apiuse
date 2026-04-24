.PHONY: test lint fuzz bench fmt tidy
test:
	go test ./... -race -count=1
lint:
	gofumpt -l -d .
	golangci-lint run ./...
fuzz:
	@echo "run individual fuzz targets, e.g. go test -fuzz=FuzzX ./..."
bench:
	go test -bench=. -benchmem ./...
fmt:
	gofumpt -w .
tidy:
	go mod tidy
