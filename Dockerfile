# Use the official Golang image as the base image
FROM golang:1.23.2-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go module files
COPY go.mod go.sum ./

# Download the Go module dependencies
RUN go mod download

# Copy the application source code
COPY . .

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -o eso-updater

# Use a minimal Alpine image for the final stage
FROM alpine:latest

# Set the working directory inside the container
WORKDIR /app

# Copy the built binary from the builder stage
COPY --from=builder /app/eso-updater .

# Copy the static directory and all contents inside
COPY --from=builder /app/static ./static

# Expose the port on which the application will run
EXPOSE 8000

# Set the entrypoint command to run the application
CMD ["/app/eso-updater"]
