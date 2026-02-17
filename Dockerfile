# Build stage
FROM golang:1.25.5-alpine AS builder

WORKDIR /app

# Install dependencies for building
RUN apk add --no-cache git gcc musl-dev

# Install templ
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Generate templ files
RUN templ generate

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build -o mercato ./cmd/mercato/main.go

# Final stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata sqlite

# Copy the binary from the builder
COPY --from=builder /app/mercato .

# Create data directory for SQLite
RUN mkdir -p /app/data

EXPOSE 8082

CMD ["./mercato"]
