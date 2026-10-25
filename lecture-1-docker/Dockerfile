FROM golang:1.22 AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o api main.go

FROM scratch
WORKDIR /app
COPY --from=builder /app/api .
EXPOSE 8080
CMD ["./api"]