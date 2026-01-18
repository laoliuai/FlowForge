# FlowForge Deployment Guide

This document describes how to deploy FlowForge services and how to integrate workloads with the FlowForge SDK.

## Overview

FlowForge is composed of multiple long-running services:

- **API Server** (`cmd/api-server`): HTTP/gRPC entry point for workflow submission and management.
- **Controller** (`cmd/controller`): Reconciles workflow state and manages task lifecycle.
- **Scheduler** (`cmd/scheduler`): Queues and schedules tasks fairly.
- **Executor** (`cmd/executor`): Launches task pods on Kubernetes and injects the FlowForge SDK.

The system relies on PostgreSQL for persistent state, Redis for queues/heartbeats, and a log/metrics endpoint for telemetry.

## Prerequisites

- Kubernetes cluster (1.24+) with a default StorageClass.
- PostgreSQL instance reachable by the FlowForge services.
- Redis instance reachable by the FlowForge services.
- Container registry for FlowForge service images and the SDK image.

## Configuration

FlowForge services load configuration from `/etc/flowforge/config.yaml` and environment variables prefixed with `FLOWFORGE_`.
At minimum, configure:

- Database connection information (`FLOWFORGE_DATABASE_*`).
- Redis connection information (`FLOWFORGE_REDIS_*`).
- Auth secrets (`FLOWFORGE_AUTH_*`).
- API, gRPC, and metrics ports (`FLOWFORGE_SERVER_*`).

Example `config.yaml`:

```yaml
server:
  http_port: 8080
  grpc_port: 9090
  metrics_port: 9091
  read_timeout: 30s

database:
  host: postgres
  port: 5432
  user: flowforge
  password: flowforge
  database: flowforge
  ssl_mode: disable

redis:
  addresses: ["redis:6379"]
  password: ""
  db: 0

kubernetes:
  in_cluster: true
  namespace: flowforge

auth:
  jwt_secret: "change-me"
  token_ttl: 24h
  task_token_ttl: 24h
```

## Build & Release

1. Build binaries:

   ```bash
   make build
   ```

2. Build container images for each service (example using Docker):

   ```bash
   docker build -t registry.example.com/flowforge/api-server -f cmd/api-server/Dockerfile .
   docker build -t registry.example.com/flowforge/controller -f cmd/controller/Dockerfile .
   docker build -t registry.example.com/flowforge/scheduler -f cmd/scheduler/Dockerfile .
   docker build -t registry.example.com/flowforge/executor -f cmd/executor/Dockerfile .
   ```

3. Push images to your registry:

   ```bash
   docker push registry.example.com/flowforge/api-server
   docker push registry.example.com/flowforge/controller
   docker push registry.example.com/flowforge/scheduler
   docker push registry.example.com/flowforge/executor
   ```

> Note: The repository does not yet include Dockerfiles; add them as part of your build pipeline.

## Deploy on Kubernetes

Below is a minimal deployment outline. You can translate this to Helm or your preferred GitOps tooling.

1. Create a namespace:

   ```bash
   kubectl create namespace flowforge
   ```

2. Create a config secret (or mount a ConfigMap):

   ```bash
   kubectl -n flowforge create secret generic flowforge-config \
     --from-file=config.yaml
   ```

3. Deploy PostgreSQL and Redis (managed services are recommended).

4. Deploy the FlowForge services. Each deployment should:

   - Mount the config file to `/etc/flowforge/config.yaml`.
   - Expose the required ports (HTTP, gRPC, metrics).
   - Include readiness/liveness probes.
   - Run with least-privilege ServiceAccounts.

5. Expose the API server via an Ingress/LoadBalancer.

## SDK Integration

The FlowForge SDK is designed to run inside task pods created by the executor. The executor injects the SDK into each pod using an init container and sets environment variables required by the SDK client.

### Build and Publish the Python SDK

1. Build the package:

   ```bash
   cd sdk/python
   python -m build
   ```

2. Publish to a package index:

   ```bash
   python -m twine upload dist/*
   ```

### Build an SDK Image (Recommended)

The executor expects an SDK image that contains the Python package at `/sdk`. Build a slim image that includes your SDK build output.

```dockerfile
FROM python:3.11-slim
WORKDIR /sdk
COPY dist/ /sdk/
RUN pip install --no-cache-dir /sdk/*.whl
```

Push the image and configure the executor to use it (e.g., via `FLOWFORGE_SDK_IMAGE`).

### Task Pod Environment Variables

Each task pod must receive the following environment variables for the SDK to initialize:

- `FLOWFORGE_TASK_ID`
- `FLOWFORGE_WORKFLOW_ID`
- `FLOWFORGE_TENANT_ID`
- `FLOWFORGE_PROJECT_ID`
- `FLOWFORGE_API_ENDPOINT`
- `FLOWFORGE_REDIS_ENDPOINT`
- `FLOWFORGE_LOG_ENDPOINT`
- `FLOWFORGE_AUTH_TOKEN`

These are automatically set by the executor when it injects the SDK. Ensure your workloads import the SDK and rely on the injected env vars.

### Example Task Code

```python
from flowforge import client

client.log("Starting task")
client.status("RUNNING", progress=10)

# Do work...

client.output("result", {"status": "ok"})
client.status("SUCCEEDED")
```

## Validation Checklist

- [ ] API server is reachable via HTTP and gRPC.
- [ ] Controller and scheduler pods are healthy.
- [ ] Executor can create task pods in the configured namespace.
- [ ] Redis and PostgreSQL connections succeed.
- [ ] Task pods report logs/metrics and status updates via the SDK.
