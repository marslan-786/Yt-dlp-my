# Step 1: Build the Go application
FROM golang:1.22-alpine AS builder
WORKDIR /app

# Copy the Go code
COPY main.go .

# Build the Go binary
RUN go build -o downloader-api main.go

# Step 2: Create the final lightweight production image
FROM alpine:latest
WORKDIR /app

# 🔥 Added nodejs for yt-dlp JavaScript execution
RUN apk update && \
    apk add --no-cache python3 ffmpeg curl nodejs

# Download the latest yt-dlp binary and make it executable
RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp && \
    chmod a+rx /usr/local/bin/yt-dlp

# Copy the compiled Go binary from the builder stage
COPY --from=builder /app/downloader-api .

# Create the downloads directory and give it read/write permissions
RUN mkdir -p downloads && chmod 777 downloads

# Expose the port the API is running on
EXPOSE 8080

# Run the API server
CMD ["./downloader-api"]
