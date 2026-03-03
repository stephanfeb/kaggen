---
name: gcp
description: Manage Google Cloud Platform resources including Compute Engine instances and networking.
---

# GCP — Google Cloud Platform Management

Use this skill to manage Google Cloud Platform resources, specifically focusing on Compute Engine instances within the `gen-lang-client-0268081540` project.

## Available Commands

### auth.sh — Authenticate with GCP

```bash
bash scripts/auth.sh
```

Authenticates the environment using the configured service account key.

### list_instances.sh — List VM Instances

```bash
bash scripts/list_instances.sh
```

Lists all VM instances in the default project (`gen-lang-client-0268081540`).

### create_instance.sh — Create e2-micro Instance

```bash
bash scripts/create_instance.sh <instance_name>
```

Creates a new `e2-micro` instance in the `asia-southeast1-a` zone.

**Example:**
```bash
bash scripts/create_instance.sh my-vm-01
```

### delete_instance.sh — Delete VM Instance

```bash
bash scripts/delete_instance.sh <instance_name>
```

Deletes the specified instance in the `asia-southeast1-a` zone.

**Example:**
```bash
bash scripts/delete_instance.sh my-vm-01
```

## Reference Table

| Parameter | Default Value |
|-----------|---------------|
| Project ID | `gen-lang-client-0268081540` |
| Zone       | `asia-southeast1-a` |
| Machine Type | `e2-micro` |

## Tips

- Ensure the service account key exists at the specified path before running `auth.sh`.
- Use `list_instances.sh` to verify the status and IP address of your instances.
- The `delete_instance.sh` script runs with `--quiet`, so it will not prompt for confirmation.
