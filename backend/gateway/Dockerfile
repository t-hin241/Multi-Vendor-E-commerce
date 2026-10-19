FROM golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder
WORKDIR /src
ENV GOWORK=off
COPY pkg ./pkg
COPY gateway/go.mod gateway/go.sum ./gateway/
RUN --mount=type=cache,target=/go/pkg/mod cd gateway && go mod download
COPY gateway ./gateway
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd gateway && CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN addgroup -S app && adduser -S app -G app && apk add --no-cache ca-certificates wget
COPY --from=builder /out/server /usr/local/bin/server
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/server"]
