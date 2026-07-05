FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
# Build the whole package (not just main.go) so new files in package main
# are always included; static binary, native arch.
RUN CGO_ENABLED=0 go build -o watched-cleanup .

FROM alpine:3.21
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
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s \
  CMD wget -qO- http://127.0.0.1:6969/healthz || exit 1
CMD ["./watched-cleanup"]
