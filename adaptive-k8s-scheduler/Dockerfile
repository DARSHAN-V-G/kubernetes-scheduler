# Stage 1: Build binary
FROM golang:1.24-alpine AS builder

WORKDIR /workspace

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o /workspace/bin/metrics-collector ./cmd/metrics-collector/main.go

# Stage 2: Minimal runtime container
FROM alpine:3.20

RUN apk --no-cache add ca-certificates curl

WORKDIR /
COPY --from=builder /workspace/bin/metrics-collector /bin/metrics-collector

USER 65532:65532

ENTRYPOINT ["/bin/metrics-collector"]
