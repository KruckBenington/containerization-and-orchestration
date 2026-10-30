FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o batch main.go

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/batch .
ENTRYPOINT ["./batch"]
