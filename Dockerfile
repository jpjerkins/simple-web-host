# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Copy go module files
COPY go.mod ./
RUN go mod tidy

# Copy source code
COPY main.go ./

# Build the binary
# CGO_ENABLED=0 for static binary
# -ldflags="-w -s" to strip debug info and reduce size
# -trimpath for reproducible builds
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -ldflags="-w -s -extldflags '-static'" \
    -trimpath \
    -o server \
    main.go

# Final stage - distroless
FROM gcr.io/distroless/static-debian12:nonroot

# Non-root user from distroless
USER nonroot:nonroot

# Copy binary from builder
COPY --from=builder --chown=nonroot:nonroot /build/server /app/server

# Set working directory
WORKDIR /app

# Expose port
EXPOSE 8080

# Run the server
ENTRYPOINT ["/app/server"]
