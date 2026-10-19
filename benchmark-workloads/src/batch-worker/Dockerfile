FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* main.go ./
RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o batch-worker main.go

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/batch-worker .

CMD ["/app/batch-worker"]
