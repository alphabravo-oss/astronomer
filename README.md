<p align="center">
  <strong>ASTRONOMER</strong><br/>
  Enterprise Kubernetes Multi-Cluster Management Platform
</p>

<p align="center">
  <a href="#license"><img src="https://img.shields.io/badge/License-AGPL_v3-blue.svg" alt="License: AGPL v3"></a>
  <a href="#"><img src="https://img.shields.io/badge/Build-Passing-brightgreen.svg" alt="Build: Passing"></a>
  <a href="#"><img src="https://img.shields.io/badge/Coverage-87%25-green.svg" alt="Coverage: 87%"></a>
  <a href="#"><img src="https://img.shields.io/badge/Django-5.0-092E20.svg" alt="Django 5.0"></a>
  <a href="#"><img src="https://img.shields.io/badge/Next.js-16-000000.svg" alt="Next.js 16"></a>
  <a href="#"><img src="https://img.shields.io/badge/Python-3.12-3776AB.svg" alt="Python 3.12"></a>
</p>

---

**Astronomer** is an enterprise-grade, open-source platform for managing multiple Kubernetes clusters from a single control plane. It provides a unified dashboard for monitoring, workload management, RBAC policy enforcement, and GitOps integration across all your clusters -- whether they run on EKS, AKS, GKE, or bare metal.

Think of it as a self-hosted alternative to Rancher, built with a modern stack and an agent-based architecture that requires **no inbound network access** to your downstream clusters.

<p align="center">
  <em>[Screenshot placeholder: Dashboard showing multi-cluster overview with health metrics, workload counts, and ArgoCD sync status]</em>
</p>

---

## Key Features

- **Multi-Cluster Management** -- Register and manage any number of Kubernetes clusters across clouds, regions, and environments from a single pane of glass.
- **Agent-Based Architecture** -- A lightweight agent runs inside each managed cluster and establishes an outbound WebSocket reverse tunnel. No VPN, no firewall rules, no inbound ports required.
- **Enterprise RBAC** -- Three-tier role-based access control (Global, Cluster, Project) with fine-grained resource/verb rules and IdP group mapping.
- **Real-Time Monitoring** -- Live cluster health dashboards powered by Prometheus integration and agent-reported metrics (CPU, memory, node/pod counts).
- **Workload Management** -- List, inspect, scale, restart, and manage Deployments, StatefulSets, DaemonSets, and Pods across all clusters.
- **Helm Chart Catalog** -- Browse, install, and manage Helm charts across clusters with a built-in chart repository browser and one-click installs.
- **Cluster Tools** -- Install and manage operational tools (monitoring, logging, security, backup, service mesh) on clusters via curated tool definitions.
- **Backup Management** -- Configure and manage backup schedules, retention policies, and restore operations across clusters.
- **Alerting** -- Define alert rules, notification channels, and escalation policies with support for multi-cluster alert aggregation.
- **Logging Configuration** -- Configure and manage centralized logging pipelines, log forwarding, and retention policies.
- **Security Scanning** -- Runtime security monitoring and vulnerability scanning with policy-based enforcement.
- **ArgoCD GitOps Integration** -- Manage ArgoCD instances and applications, trigger syncs, view deployment history, and inspect rendered manifests.
- **Service Proxy** -- Proxy HTTP requests to ClusterIP services inside managed clusters through the agent tunnel, enabling access to internal dashboards and UIs.
- **SSO/OIDC Authentication** -- Authenticate via GitHub, Google, or any OIDC/SAML provider. Personal API tokens with scoping and expiration.
- **Real-Time WebSocket Updates** -- Live cluster status changes, health events, and workload updates pushed to the browser via Django Channels.
- **Kubernetes API Proxy** -- Transparently proxy `kubectl`-style API requests to any managed cluster through the agent tunnel.
- **Audit Logging** -- Immutable audit trail of all significant actions for compliance and security analysis.

---

## Architecture Overview

Astronomer uses a hub-and-spoke architecture. The **management plane** runs the API server, frontend, and supporting infrastructure. Each managed cluster runs a lightweight **agent** that dials home over a WebSocket reverse tunnel.

```
                        Management Plane
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  ┌──────────┐    ┌──────────────┐    ┌───────────────┐  │
  │  │ Next.js  │    │  Django API  │    │  Celery Beat  │  │
  │  │ Frontend ├───>│  (Daphne)    │<───┤  + Workers    │  │
  │  │ :3000    │    │  :8000       │    │               │  │
  │  └──────────┘    └──────┬───────┘    └───────┬───────┘  │
  │                         │                    │          │
  │  ┌──────────┐  ┌───────┴──────────┐         │          │
  │  │ Website  │  │                  │         │          │
  │  │ :3001    │  │                  │         │          │
  │  └──────────┘  │                  │         │          │
  │        ┌───────┴──────┐    ┌─────┴─────┐   │          │
  │        │ PostgreSQL   │    │   Redis   │<──┘          │
  │        │ :5432        │    │   :6379   │              │
  │        └──────────────┘    └───────────┘              │
  │                                                          │
  │  ┌──────────┐                                           │
  │  │ Registry │  (Docker registry for agent images)       │
  │  │ :5000    │                                           │
  │  └──────────┘                                           │
  │                                                          │
  └────────────────────────┬─────────────────────────────────┘
                           │
                    Nginx Reverse Proxy
                    (TLS Termination)
                           │
               ────────────┼──────────────
              │            │              │
              ▼            ▼              ▼
       ┌─────────┐  ┌─────────┐   ┌─────────┐
       │  Agent  │  │  Agent  │   │  Agent  │
       │ Cluster │  │ Cluster │   │ Cluster │
       │   A     │  │   B     │   │   C     │
       └────┬────┘  └────┬────┘   └────┬────┘
            │             │             │
            ▼             ▼             ▼
       K8s API       K8s API       K8s API
```

Each agent:
1. Establishes a persistent WebSocket connection to the management server.
2. Sends periodic heartbeats and resource metrics.
3. Proxies Kubernetes API requests from the management server to the local cluster.
4. Handles Helm operations (install, upgrade, uninstall) for chart catalog and tools.
5. Proxies HTTP requests to ClusterIP services for the service proxy feature.
6. Streams container logs and executes commands in pods.
7. Applies RBAC policies pushed from the management server.

For a detailed architecture deep-dive, see [docs/architecture.md](docs/architecture.md).

---

## Quick Start

### Prerequisites

- Docker >= 24.0
- Docker Compose >= 2.20
- Git

### 1. Clone and Configure

```bash
git clone https://github.com/astronomer/astronomer.git
cd astronomer

# Copy the example environment file and review the settings
cp .env.example .env
```

### 2. Build and Start

```bash
# First-time setup: build images, run migrations, create admin user
make setup

# Start all services in development mode
make dev
```

### 3. Access the Platform

| Service            | URL                                       |
|--------------------|-------------------------------------------|
| Frontend (UI)      | [http://localhost:3000](http://localhost:3000) |
| Website            | [http://localhost:3001](http://localhost:3001) |
| API                | [http://localhost/api/v1/](http://localhost/api/v1/) |
| API Docs (Swagger) | [http://localhost/api/v1/schema/swagger/](http://localhost/api/v1/schema/swagger/) |
| API Docs (ReDoc)   | [http://localhost/api/v1/schema/redoc/](http://localhost/api/v1/schema/redoc/) |
| Django Admin       | [http://localhost/admin/](http://localhost/admin/) |

### 4. Register Your First Cluster

1. Log in to the dashboard.
2. Navigate to **Clusters > Add Cluster**.
3. Fill in the cluster details and click **Register**.
4. Copy the generated agent install manifest.
5. Apply it to your downstream cluster: `kubectl apply -f agent-install.yaml`

The agent will connect within seconds and the cluster status will transition to **Active**.

---

## Configuration

All configuration is managed through environment variables. Copy `.env.example` to `.env` and customize the values for your environment.

For a complete reference of every setting, see [docs/configuration.md](docs/configuration.md).

Key variables:

| Variable              | Description                                 | Default                        |
|-----------------------|---------------------------------------------|--------------------------------|
| `SECRET_KEY`          | JWT HMAC signing key (multi-key supported via comma) | *(generate a random value)* |
| `ASTRONOMER_ENCRYPTION_KEY` | Fernet key wrapping SSO/agent secrets | *(required for production)* |
| `DATABASE_URL`        | PostgreSQL connection string                | `postgres://...@postgres:5432` |
| `REDIS_URL`           | Redis connection string                     | `redis://redis:6379/0`         |
| `SERVER_URL`          | External base URL of this install           | *(empty)*                      |
| `DEBUG`               | Enable debug logging                        | `false`                        |
| `ENV`                 | Environment label (development / production)| `development`                  |

---

## Production Deployment

For production, Astronomer provides Kubernetes manifests in the `k8s/` directory. The deployment includes:

- High-availability backend with 3 replicas and pod anti-affinity
- Horizontal Pod Autoscaler for the backend
- Init containers for database migrations
- TLS termination via Ingress
- Separate Celery worker and beat deployments

```bash
# Deploy to Kubernetes
make k8s-deploy

# Check status
make k8s-status
```

For a complete production deployment guide including TLS setup, database configuration, monitoring, and backup procedures, see [docs/deployment.md](docs/deployment.md).

---

## Agent Installation

The Astronomer agent is a lightweight Python process that runs inside each managed cluster. It requires no inbound network access -- it connects outbound to the management server over WebSocket.

```bash
# Option 1: Via the management UI (recommended)
# Navigate to Clusters > [Your Cluster] > Register and apply the generated manifest

# Option 2: Via kubectl with a pre-generated manifest
kubectl apply -f https://your-astronomer-server/api/v1/clusters/<id>/register/manifest

# Option 3: Manual installation
kubectl apply -f agent/manifests/install.yaml
```

For detailed installation instructions, configuration options, and troubleshooting, see [docs/agent-installation.md](docs/agent-installation.md).

---

## Tech Stack

| Layer          | Technology                                                         |
|----------------|--------------------------------------------------------------------|
| **Frontend**   | Next.js 16, React, TypeScript, Tailwind CSS, shadcn/ui, Zustand, React Query |
| **Website**    | Next.js 15, React, TypeScript, Tailwind CSS (marketing site and blog) |
| **Backend**    | Django 5, Django REST Framework, Django Channels, Daphne (ASGI)    |
| **Auth**       | django-allauth (GitHub, Google, OIDC), SimpleJWT, NextAuth.js      |
| **Database**   | PostgreSQL 16                                                      |
| **Cache/Queue**| Redis 7, Celery 5, django-celery-beat                              |
| **Agent**      | Python 3.12, websockets, httpx, kubernetes-client, structlog       |
| **API Docs**   | drf-spectacular (OpenAPI 3.0), Swagger UI, ReDoc                   |
| **Proxy**      | Nginx (TLS termination, rate limiting, WebSocket proxying)         |
| **Registry**   | Docker Registry v2 (serves agent images to managed clusters)       |
| **Monitoring** | Prometheus client, Sentry SDK                                      |
| **Infra**      | Docker Compose, Kubernetes manifests                               |

---

## Project Structure

```
astronomer/
├── backend/                  # Django backend application
│   ├── astronomer/           #   Django project settings and configuration
│   │   ├── asgi.py           #     ASGI entrypoint (HTTP + WebSocket routing)
│   │   ├── celery.py         #     Celery app and beat schedule
│   │   ├── routing.py        #     WebSocket URL routing
│   │   ├── urls.py           #     REST API URL configuration
│   │   └── wsgi.py           #     WSGI entrypoint (fallback)
│   ├── apps/                 #   Django applications
│   │   ├── agents/           #     WebSocket tunnel consumers and protocol
│   │   ├── alerting/         #     Alert rules, notifications, and escalation
│   │   ├── argocd/           #     ArgoCD instance and application management
│   │   ├── audit/            #     Audit log recording and querying
│   │   ├── authentication/   #     SSO, API tokens, user profiles
│   │   ├── backups/          #     Backup schedules, retention, and restores
│   │   ├── catalog/          #     Helm chart repos, charts, and installs
│   │   ├── clusters/         #     Cluster CRUD, registration, health, service proxy
│   │   ├── core/             #     Base models, audit logging, settings views
│   │   ├── logging_config/   #     Centralized logging pipeline configuration
│   │   ├── monitoring/       #     Prometheus integration and metrics queries
│   │   ├── projects/         #     Project (namespace group) management
│   │   ├── rbac/             #     Three-tier RBAC (Global/Cluster/Project)
│   │   ├── resources/        #     Kubernetes resource browsing and management
│   │   ├── security/         #     Security scanning and runtime policies
│   │   ├── tools/            #     Cluster tool catalog and installation
│   │   └── workloads/        #     Workload listing, scaling, restarting
│   ├── conftest.py           #   Shared pytest fixtures
│   ├── manage.py             #   Django management command entrypoint
│   └── requirements.txt      #   Python dependencies
├── frontend/                 # Next.js dashboard application
│   ├── src/
│   │   ├── app/              #     Next.js App Router pages
│   │   │   ├── auth/         #       Authentication pages
│   │   │   ├── bootstrap/    #       First-run bootstrap flow
│   │   │   └── dashboard/    #       Main dashboard
│   │   │       ├── alerting/       #   Alert management
│   │   │       ├── argocd/         #   ArgoCD integration
│   │   │       ├── backups/        #   Backup management
│   │   │       ├── catalog/        #   Helm chart catalog
│   │   │       ├── clusters/       #   Cluster overview and detail
│   │   │       │   └── [id]/tools/ #   Per-cluster tool management
│   │   │       ├── logging/        #   Logging configuration
│   │   │       ├── monitoring/     #   Monitoring dashboards
│   │   │       ├── networking/     #   Network policies
│   │   │       ├── projects/       #   Project management
│   │   │       ├── rbac/           #   RBAC management
│   │   │       ├── security/       #   Security scanning
│   │   │       ├── settings/       #   Platform settings
│   │   │       ├── storage/        #   Storage management
│   │   │       ├── tools/          #   Tool catalog browser
│   │   │       └── workloads/      #   Workload management
│   │   ├── components/       #     React components
│   │   │   ├── ui/           #       shadcn/ui base components
│   │   │   ├── clusters/     #       Cluster-specific components
│   │   │   ├── workloads/    #       Workload management components
│   │   │   ├── monitoring/   #       Monitoring and charts
│   │   │   ├── argocd/       #       ArgoCD integration components
│   │   │   └── rbac/         #       RBAC management components
│   │   ├── lib/              #     Utility functions and API client
│   │   ├── styles/           #     Global CSS and Tailwind config
│   │   └── types/            #     TypeScript type definitions
│   └── package.json
├── website/                  # Public-facing marketing website
│   ├── src/app/(frontend)/   #   Next.js marketing pages
│   │   ├── about/            #     About page
│   │   ├── blog/             #     Blog listing and post pages
│   │   ├── docs/             #     Documentation landing
│   │   ├── features/         #     Features page
│   │   ├── onboarding/       #     Onboarding flow
│   │   └── pricing/          #     Pricing page
│   ├── apps/                 #   Django backend (mirror of backend/apps)
│   ├── astronomer/           #   Django settings for website context
│   ├── manage.py
│   └── requirements.txt
├── agent/                    # Cluster agent (WebSocket reverse tunnel)
│   ├── astronomer_agent/
│   │   ├── main.py           #     CLI entrypoint (Click)
│   │   ├── tunnel.py         #     WebSocket tunnel with stream multiplexing
│   │   ├── protocol.py       #     Wire protocol (message types, serialization)
│   │   ├── k8s_proxy.py      #     Kubernetes API request proxy
│   │   ├── service_proxy.py  #     HTTP proxy to ClusterIP services
│   │   ├── helm_handler.py   #     Helm chart install/upgrade/uninstall
│   │   ├── exec_handler.py   #     Pod command execution handler
│   │   ├── log_stream_handler.py  # Container log streaming
│   │   ├── health.py         #     Health reporting and metrics collection
│   │   ├── rbac_sync.py      #     RBAC manifest reconciliation
│   │   └── config.py         #     Agent configuration dataclass
│   ├── manifests/
│   │   └── install.yaml.template  # Templated agent install manifest
│   ├── tests/                #     Agent unit tests
│   └── setup.py
├── k8s/                      # Kubernetes deployment manifests
│   ├── namespace.yaml
│   ├── backend/              #     Backend deployment, service, HPA, ingress
│   ├── frontend/             #     Frontend deployment and service
│   ├── postgres/             #     PostgreSQL deployment and service
│   └── redis/                #     Redis deployment and service
├── nginx/                    # Nginx reverse proxy configuration
│   ├── nginx.conf            #     Full Nginx config with TLS and rate limiting
│   └── Dockerfile
├── content/                  # Website content (blog posts, docs)
├── docs/                     # Documentation
├── scripts/                  # Helper scripts (DB init, etc.)
├── docker-compose.yml        # Development environment (10 services)
├── Makefile                  # Development commands
└── .env.example              # Environment variable reference
```

---

## API Overview

Astronomer exposes a RESTful API under `/api/v1/` with full OpenAPI 3.0 documentation.

| Endpoint                          | Description                              |
|-----------------------------------|------------------------------------------|
| `POST /api/v1/auth/login/`       | Authenticate and obtain JWT tokens       |
| `POST /api/v1/auth/tokens/`      | Create a personal API token              |
| `GET  /api/v1/auth/me/`          | Get current user profile and roles       |
| `GET  /api/v1/clusters/`         | List all clusters                        |
| `POST /api/v1/clusters/`         | Register a new cluster                   |
| `POST /api/v1/clusters/{id}/register/` | Generate agent install manifest    |
| `GET  /api/v1/clusters/{id}/health/`   | Get cluster health snapshot        |
| `*/api/v1/clusters/{id}/proxy/service/...` | Proxy requests to ClusterIP services |
| `GET  /api/v1/workloads/workloads/{cluster_id}/` | List workloads         |
| `POST /api/v1/workloads/workloads/{cluster_id}/.../scale` | Scale a workload |
| `GET  /api/v1/catalog/charts/`   | List Helm charts across repos            |
| `GET  /api/v1/catalog/repos/`    | List configured Helm repositories        |
| `GET  /api/v1/tools/`            | List available cluster tools             |
| `GET  /api/v1/tools/{slug}/`     | Get tool detail and install config       |
| `GET  /api/v1/monitoring/metrics/cluster-overview/{cluster_id}/` | Cluster metrics |
| `POST /api/v1/monitoring/metrics/query/{cluster_id}/` | Execute PromQL query |
| `GET  /api/v1/argocd/applications/` | List ArgoCD applications              |
| `POST /api/v1/argocd/applications/{id}/sync/` | Trigger ArgoCD sync       |
| `GET  /api/v1/alerting/rules/`   | List alert rules                         |
| `GET  /api/v1/backups/`          | List backup configurations               |
| `GET  /api/v1/logging/pipelines/`| List logging pipelines                   |
| `GET  /api/v1/security/scans/`   | List security scan results               |
| `GET  /api/v1/audit/`            | Query audit logs                         |
| `GET  /api/v1/rbac/my-roles/`    | Get current user's role bindings         |
| `GET  /api/v1/rbac/my-roles/check/` | Check a specific permission           |
| `GET  /api/v1/settings/general`  | Get platform settings                    |
| `GET  /api/v1/activity`          | Get activity feed                        |

Interactive API documentation is available at:
- **Swagger UI**: `/api/v1/schema/swagger/`
- **ReDoc**: `/api/v1/schema/redoc/`

For the complete API reference, see [docs/api-reference.md](docs/api-reference.md).

---

## Docker Compose Services

The development environment runs 10 services:

| Service          | Container                | Port | Description                          |
|------------------|--------------------------|------|--------------------------------------|
| `postgres`       | `astronomer-postgres`    | 5432 | PostgreSQL 16 primary datastore      |
| `redis`          | `astronomer-redis`       | 6379 | Cache, message broker, channel layer |
| `backend`        | `astronomer-backend`     | 8000 | Django ASGI application (Daphne)     |
| `celery-worker`  | `astronomer-celery-worker` | -  | Async task processing                |
| `celery-beat`    | `astronomer-celery-beat` | -    | Periodic task scheduler              |
| `frontend`       | `astronomer-frontend`    | 3000 | Next.js dashboard application        |
| `website`        | `astronomer-website`     | 3001 | Public marketing website             |
| `nginx`          | `astronomer-nginx`       | 80/443 | Reverse proxy and TLS termination  |
| `registry`       | `astronomer-registry`    | 5000 | Docker registry for agent images     |

---

## Development

### Useful Commands

```bash
make help                # Show all available commands
make dev                 # Start all services (foreground)
make up                  # Start all services (background)
make down                # Stop all services
make test                # Run all tests (backend + frontend)
make test-backend        # Run backend tests with pytest
make test-frontend       # Run frontend tests with Jest
make lint                # Run all linters (ruff, eslint)
make logs                # Tail all service logs
make shell               # Open Django interactive shell
make dbshell             # Open PostgreSQL shell
make migrate             # Run database migrations
make makemigrations      # Create new migration files
```

### Running Tests

```bash
# Backend tests (pytest)
make test-backend

# Backend tests with coverage
make test-backend-cov

# Frontend tests (Jest)
make test-frontend
```

---

## Contributing

Contributions are welcome. Please follow these guidelines:

1. **Fork** the repository and create a feature branch from `main`.
2. **Write tests** for any new functionality.
3. **Follow the code style**: `ruff` for Python, `eslint` + `prettier` for TypeScript.
4. **Run the full test suite** before submitting: `make test && make lint`.
5. **Write clear commit messages** describing the change.
6. **Open a pull request** with a description of what changed and why.

### Code Style

- **Python**: Enforced by [Ruff](https://docs.astral.sh/ruff/) (linting + formatting).
- **TypeScript**: Enforced by ESLint with the Next.js configuration and Prettier.
- **Commits**: Use conventional commit messages where possible.

---

## License

Copyright 2024-2026 AlphaBravo, Inc.

This program is free software: you can redistribute it and/or modify it under the terms of the GNU Affero General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the [GNU Affero General Public License](LICENSE) for more details.

---

## Documentation

- [Architecture Overview](docs/architecture.md)
- [Production Deployment Guide](docs/deployment.md)
- [API Reference](docs/api-reference.md)
- [Agent Installation Guide](docs/agent-installation.md)
- [Configuration Reference](docs/configuration.md)
