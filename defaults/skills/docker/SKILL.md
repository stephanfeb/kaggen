---
name: docker
description: A skill to build and deploy Docker containers. It can handle building images, managing volumes, and running containers.
---

# Docker — Container Management

Use this skill to build Docker images and to run, stop, and manage Docker containers. This is primarily used for deploying applications that I have developed.

## Available Commands

### deploy.sh — Build and Run a Docker Container

This script automates the process of building a Docker image from a project directory, creating a persistent volume, and running the container with the correct port and volume mappings. It will also automatically stop and remove any existing container with the same name to prevent conflicts.

```bash
bash scripts/deploy.sh --project-dir <path> --container-name <name> --port-mapping <host:container> [--volume-name <name>] [--volume-path <path>]
```

**Options:**
- `--project-dir <path>` — **Required.** The absolute path to the directory containing the `Dockerfile`.
- `--container-name <name>` — **Required.** The name for the new Docker image and running container.
- `--port-mapping <host:container>` — **Required.** The port mapping from the host to the container (e.g., `8080:80`).
- `--volume-name <name>` — *Optional.* The name of the Docker volume to create for persistent data.
- `--volume-path <path>` — *Optional.* The path inside the container where the volume should be mounted (e.g., `/app/data`).

**Important:** `--volume-name` and `--volume-path` must be used together.

## Examples

### Deploying a simple web application

Builds the Dockerfile in `~/claude-projects/my-app`, names the container `my-cool-app`, and maps port 8080 on the host to port 80 in the container.

```bash
bash scripts/deploy.sh \\
  --project-dir "/Users/stephanfeb/claude-projects/my-app" \\
  --container-name "my-cool-app" \\
  --port-mapping "8080:80"
```

### Deploying an application with a persistent database

Builds the `sailboat-app-v2`, names the container, maps the port, and crucially, creates a Docker volume named `sailboat-data-v2` and mounts it to `/app/data` inside the container. This ensures the SQLite database will persist even if the container is removed and redeployed.

```bash
bash scripts/deploy.sh \\
  --project-dir "/Users/stephanfeb/claude-projects/sailboat-dashboard-v2" \\
  --container-name "sailboat-app-v2" \\
  --port-mapping "5001:5000" \\
  --volume-name "sailboat-data-v2" \\
  --volume-path "/app/data"
```
