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

tidy:
	go mod tidy

check: vet test race
	test -z "$$(gofmt -l cmd internal)"

# Local private-plugin import bundle only; never uploaded automatically.
trmnl-package:
	mkdir -p dist
	zip -j -FS dist/berlin-transit-trmnl.zip integrations/trmnl/settings.yml integrations/trmnl/full.liquid integrations/trmnl/half_horizontal.liquid integrations/trmnl/half_vertical.liquid integrations/trmnl/quadrant.liquid

GORELEASER ?= goreleaser
KO ?= ko
HELM ?= helm
PLATFORM ?= linux/$(shell go env GOARCH)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: release-check release-snapshot image image-multiarch helm-check helm-package
release-check:
	$(GORELEASER) check
	go -C tools test ./release

release-snapshot: release-check
	$(GORELEASER) release --snapshot --clean --skip=publish,sign

image:
	KO_DOCKER_REPO=ko.local/berlin-transit $(KO) build --bare --platform $(PLATFORM) --tags local --image-user=65532:65532 --image-label "org.opencontainers.image.created=$(BUILD_DATE)" ./cmd/berlin-transit

image-multiarch:
	mkdir -p dist
	KO_DOCKER_REPO=example.invalid/berlin-transit $(KO) build --bare --push=false --platform linux/amd64,linux/arm64 --image-user=65532:65532 --image-label "org.opencontainers.image.created=$(BUILD_DATE)" --oci-layout-path dist/oci ./cmd/berlin-transit

helm-check:
	$(HELM) lint --strict charts/berlin-transit
	$(HELM) lint --strict charts/berlin-transit -f charts/berlin-transit/examples/release.yaml
	$(HELM) lint --strict charts/berlin-transit -f charts/berlin-transit/examples/release.yaml -f charts/berlin-transit/examples/trmnl.yaml
	HELM=$(abspath $(shell command -v $(HELM))) go test -count=1 -run '^TestChart$$' ./internal/packaging

helm-package:
	mkdir -p dist/charts
	$(HELM) package charts/berlin-transit --destination dist/charts

.PHONY: compose-check
compose-check:
	COMPOSE_CHECK=1 go test -count=1 -run '^TestCompose$$' ./internal/packaging
