FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download || true
COPY main.go ./
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o worker main.go

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/worker .
CMD ["./worker"]
