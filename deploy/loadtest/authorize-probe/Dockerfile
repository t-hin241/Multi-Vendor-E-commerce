FROM golang:1.27.2-alpine3.24@sha256:85dc1069ac644ea3c527b177303a406eb3358192816cd7f9e5848eb658851673 AS builder
WORKDIR /src/pkg
ENV GOWORK=off
COPY backend/pkg ./
COPY deploy/loadtest/authorize-probe/main.go /src/probe/main.go
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -o /out/probe /src/probe/main.go
FROM scratch
COPY --from=builder /out/probe /probe
USER 65532:65532
ENTRYPOINT ["/probe"]
