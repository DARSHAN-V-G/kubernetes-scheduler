FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod main.go ./
RUN go mod tidy && go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o core-api main.go

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/core-api .

EXPOSE 3000
CMD ["/app/core-api"]
