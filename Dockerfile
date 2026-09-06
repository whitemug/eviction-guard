# Build the manager binary
FROM golang:1.27.1 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
# Stay on the image's Go; do not download a different toolchain or rewrite go.mod.
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -mod=readonly -a -o manager cmd/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532
ENTRYPOINT ["/manager"]
