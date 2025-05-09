
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

# Create fabric group and user with UID 1000 and home directory /home/fabric
RUN addgroup -g 1000 fabric && \
    adduser -D -u 1000 -G fabric -h /home/fabric fabric

# Ensure config directories exist for fabric user
RUN mkdir -p /home/fabric/.config/fabric/patterns

# Copy local patterns into container patterns directory
COPY patterns/. /home/fabric/.config/fabric/patterns

# Copy the binary from builder into the fabric user's home
COPY --from=builder /app/fabric /home/fabric/fabric

# Set ownership of home directory to fabric user
RUN chown -R fabric:fabric /home/fabric

# Expose port 8080
EXPOSE 8080

# Switch to fabric user
USER fabric

# Set working directory to fabric user's home
WORKDIR /home/fabric

# Run the binary with debug output
ENTRYPOINT ["./fabric"]
CMD ["--serve"]


