# syntax=docker/dockerfile:1

FROM golang:1.21 as builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/universal-proxy-controller ./

FROM gcr.io/distroless/static:nonroot
USER 65532:65532
COPY --from=builder /out/universal-proxy-controller /manager
ENTRYPOINT ["/manager"]