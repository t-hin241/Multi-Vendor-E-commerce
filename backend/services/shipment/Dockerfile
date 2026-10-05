FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder
WORKDIR /src
ENV GOWORK=off
COPY pkg ./pkg
COPY services/shipment/go.mod services/shipment/go.sum ./services/shipment/
RUN --mount=type=cache,target=/go/pkg/mod cd services/shipment && go mod download
COPY services/shipment ./services/shipment
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/shipment && CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN addgroup -S app && adduser -S app -G app && apk add --no-cache ca-certificates wget
COPY --from=builder /out/server /usr/local/bin/server
USER app
EXPOSE 8088
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3   CMD wget -qO- http://localhost:8088/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]
