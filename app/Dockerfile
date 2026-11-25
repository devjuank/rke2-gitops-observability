# syntax=docker/dockerfile:1

# Build stage runs on the builder's native platform and cross-compiles,
# which is much faster than emulating the target arch with QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27.4 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# Static binary (no libc) so it runs on distroless/static.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/workload-api .

# Final image: no shell, no package manager, runs as UID 65532.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/workload-api /workload-api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/workload-api"]
