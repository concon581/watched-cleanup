FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN GOOS=linux GOARCH=amd64 go build -o watched-cleanup main.go

FROM alpine:latest
WORKDIR /app
RUN apk --no-cache add ca-certificates
# Copy binary and templates
COPY --from=builder /app/watched-cleanup .
COPY --from=builder /app/templates ./templates
# Create a non-root user and set permissions
RUN addgroup -g 114 appgroup && \
    adduser -D -u 992 -G appgroup appuser && \
    chown -R appuser:appgroup /app
USER appuser
EXPOSE 6969
CMD ["./watched-cleanup"]