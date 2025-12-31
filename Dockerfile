FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN GOOS=linux GOARCH=amd64 go build -o watched-cleanup main.go

FROM alpine:latest
WORKDIR /root/
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/watched-cleanup .
EXPOSE 6969
CMD ["./watched-cleanup"]