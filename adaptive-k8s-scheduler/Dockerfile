# -----------------------------------------------------------------------------
# Stage 1: Build the Go binaries
# -----------------------------------------------------------------------------
FROM golang:1.24-alpine AS builder

WORKDIR /workspace

# Install ca-certificates and git
RUN apk --no-cache add ca-certificates git

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree
COPY . .

# Build both scheduler and standalone metrics-collector binaries
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bin/adaptive-scheduler ./cmd/scheduler
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bin/metrics-collector ./cmd/metrics-collector

# -----------------------------------------------------------------------------
# Stage 2: Minimal runtime image
# -----------------------------------------------------------------------------
FROM alpine:3.20

RUN apk --no-cache add ca-certificates curl

WORKDIR /

COPY --from=builder /bin/adaptive-scheduler /bin/adaptive-scheduler
COPY --from=builder /bin/metrics-collector /bin/metrics-collector

USER 65532:65532

ENTRYPOINT ["/bin/adaptive-scheduler"]
