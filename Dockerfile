FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -o /out/berlin-transit ./cmd/berlin-transit

# scratch is Docker's empty filesystem, with no external runtime image to trust.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/berlin-transit /berlin-transit
USER 65532:65532
ENTRYPOINT ["/berlin-transit"]
