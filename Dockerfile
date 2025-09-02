# MULTISTAGE BUILD: Build the Go application
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy the Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go application
# The -a flag forces a rebuild of all packages
# The -installsuffix cgo prevents cgo-related dependencies
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /go-app .

# Stage 2: Create the final, minimal image
FROM scratch

# Copy the built binary and the credentials file
COPY --from=builder /go-app /go-app

# Copy the Certs too, otherwise every request returns x509: certificate signed by unknown authority
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Define the entry point
ENTRYPOINT ["/go-app"]