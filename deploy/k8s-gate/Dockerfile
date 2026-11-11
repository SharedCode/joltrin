# syntax=docker/dockerfile:1
# Build from the repository root: docker build -f deploy/k8s-gate/Dockerfile .
FROM golang:1.27.4-alpine AS builder
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sop-k8s-gate ./cmd/sop-k8s-gate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/sop-k8s-gate /usr/local/bin/sop-k8s-gate
USER nonroot:nonroot
EXPOSE 8443
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/sop-k8s-gate"]
