# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder
WORKDIR /src
ENV GOWORK=off
COPY pkg ./pkg
COPY services/vendor/go.mod services/vendor/go.sum ./services/vendor/
RUN cd services/vendor && go mod download
COPY services/vendor ./services/vendor
RUN cd services/vendor && CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.20
RUN addgroup -S app && adduser -S app -G app && apk add --no-cache ca-certificates wget
COPY --from=builder /out/server /usr/local/bin/server
USER app
EXPOSE 8082
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3   CMD wget -qO- http://localhost:8082/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]
