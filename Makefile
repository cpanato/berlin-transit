.PHONY: build fmt vet test race coverage check tidy trmnl-package

build:
	mkdir -p dist
	go build -trimpath -o dist/berlin-transit ./cmd/berlin-transit

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	awk -f scripts/check-coverage.awk coverage.out

tidy:
	go mod tidy

check: vet test race
	test -z "$$(gofmt -l cmd internal)"

# Local private-plugin import bundle only; never uploaded automatically.
trmnl-package:
	mkdir -p dist
	zip -j -FS dist/berlin-transit-trmnl.zip integrations/trmnl/settings.yml integrations/trmnl/full.liquid integrations/trmnl/half_horizontal.liquid integrations/trmnl/half_vertical.liquid integrations/trmnl/quadrant.liquid
