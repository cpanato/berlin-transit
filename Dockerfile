FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
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
