# ---- Builder Stage ----
FROM golang:1.25.4-alpine AS builder

# Install build dependencies AND CA certificates
RUN apk update && apk add --no-cache git build-base ca-certificates
RUN update-ca-certificates

# Set working directory
WORKDIR /app

# Copy go mod and sum files FIRST
COPY go.mod go.sum ./
# Download dependencies - this layer is cached if go.mod/go.sum don't change
RUN go mod download

# Copy ONLY the necessary source directories
COPY abb/ ./abb/
COPY cmd/ ./cmd/
COPY http/ ./http/
COPY internal/ ./internal/
COPY solana/ ./solana/
COPY worker/ ./worker/
COPY db/ ./db/

# Build the application binary statically for Linux
# Use ldflags to strip debug info and reduce binary size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/abb cmd/abb/*.go

# ---- Final Stage ----
FROM alpine:latest

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

WORKDIR /app

# Copy the built binary from the builder stage
COPY --from=builder /bin/abb /abb

# Expose the port the server listens on
EXPOSE 8080

# Set the default entrypoint command to run the server
# Kubernetes worker deployment should override this with command/args
CMD ["/abb", "run", "http-server"]
