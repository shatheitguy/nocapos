# syntax=docker/dockerfile:1.7
#
# Multi-arch build (cross-compiled, no QEMU needed for the Go step):
#   docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 -t alfaos/alfad:dev .

ARG GO_VERSION=1.25

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
WORKDIR /src

COPY . .
# go.sum is generated on first build if it has not been committed yet;
# commit it afterwards for reproducible builds.
RUN --mount=type=cache,target=/go/pkg/mod \
    [ -f go.sum ] || go mod tidy
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/alfad ./cmd/alfad \
 && mkdir -p /out/data

# distroless/base (not static) ships glibc, which the nvidia-smi binary injected
# by the NVIDIA Container Toolkit needs to run.
FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/alfad /alfad
# Pre-create /data owned by nonroot so a fresh named volume inherits it.
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/alfad"]
CMD ["serve"]
