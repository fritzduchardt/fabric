# Use official golang image as builder
FROM golang:1.24.2-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o fabric

# Use alpine as final base image
FROM alpine:latest

# Copy the binary from builder
COPY --from=builder /app/fabric /fabric

# Ensure clean config directories
RUN mkdir -p /home/root/.config/fabric/patterns

# Copy local patterns into container patterns directory
COPY patterns/. /home/root/.config/fabric/patterns

# Expose port 8080
EXPOSE 8080

# Run the binary with debug output
ENTRYPOINT ["/fabric"]
CMD ["--serve"]
