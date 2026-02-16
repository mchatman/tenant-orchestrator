# Tenant Orchestrator

A lightweight service that provisions and manages per-tenant [OpenClaw](https://openclaw.rocks) instances on Kubernetes via the OpenClaw CRD operator.

## Quick Start

```bash
# Build
go build -o tenant-provisioner ./cmd

# Run (requires a valid kubeconfig or in-cluster service account)
export TENANT_NAMESPACE=tenants     # default: tenants
export TENANT_DOMAIN=wareit.ai      # default: wareit.ai
export PORT=8080                    # default: 8080
./tenant-provisioner
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `TENANT_NAMESPACE` | `tenants` | Kubernetes namespace for tenant instances |
| `TENANT_DOMAIN` | `wareit.ai` | Public domain suffix for instance URLs |
| `PORT` | `8080` | HTTP listen port |
| `KUBECONFIG_BASE64` | — | Base64-encoded kubeconfig (for non-cluster deploys) |
| `ANTHROPIC_API_KEY` | — | Injected into tenant instances |
| `OPENAI_API_KEY` | — | Injected into tenant instances |

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/tenants/{tenant-id}/instance` | Create an instance |
| `GET` | `/tenants/{tenant-id}/instance` | Get instance status |
| `DELETE` | `/tenants/{tenant-id}/instance` | Delete an instance |

`tenant-id` must be a valid UUID.

## Docker

```bash
docker build -t tenant-provisioner .
docker run -p 8080:8080 \
  -e KUBECONFIG_BASE64="$(base64 < kubeconfig-sa.yaml)" \
  tenant-provisioner
```

## Project Structure

```
cmd/main.go              – Entrypoint, routing, graceful shutdown
api/handlers.go          – HTTP handlers
internal/config/config.go – Centralised configuration
internal/k8s/manager.go  – Kubernetes CRD operations
```
