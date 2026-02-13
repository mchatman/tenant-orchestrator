FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o tenant-orchestrator cmd/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/tenant-orchestrator .
# Copy kubeconfig for service account authentication
COPY kubeconfig-sa.yaml /root/.kube/config

EXPOSE 8080

CMD ["./tenant-orchestrator"]