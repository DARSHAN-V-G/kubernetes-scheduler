FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod main.go ./
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o traffic-simulator main.go

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/traffic-simulator .

ENTRYPOINT ["/app/traffic-simulator"]
