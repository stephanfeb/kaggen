---
name: plane
description: Manage workspaces, projects, and issues in Plane using the API
secrets: [plane-api-key]
---

# Plane — Project Management API

Use this skill to manage workspaces, projects, and issues in Plane.

## Configuration

- **Base URL**: `http://localhost:8089/api/v1` (or your self-hosted instance)
- **Auth**: Uses `plane-api-key` secret with `X-API-Key` header

## Operations

### List Workspaces

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/
  method: GET
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
```

Returns array of workspaces with `slug`, `name`, `id`.

### List Projects

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/{workspace_slug}/projects/
  method: GET
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
```

Replace `{workspace_slug}` with the workspace slug from List Workspaces.

### List Issues

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/{workspace_slug}/projects/{project_id}/issues/
  method: GET
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
```

### Get Issue Details

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/{workspace_slug}/projects/{project_id}/issues/{issue_id}/
  method: GET
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
```

### Create Issue

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/{workspace_slug}/projects/{project_id}/issues/
  method: POST
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
  body: {
    "name": "Issue title",
    "description_html": "<p>Issue description</p>",
    "priority": "medium",
    "state": "backlog"
  }
```

**Priority values**: `urgent`, `high`, `medium`, `low`, `none`

### Update Issue

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/{workspace_slug}/projects/{project_id}/issues/{issue_id}/
  method: PATCH
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
  body: {
    "name": "Updated title",
    "priority": "high",
    "state": "in_progress"
  }
```

Only include fields you want to update.

### Delete Issue

```
http_request:
  url: http://localhost:8089/api/v1/workspaces/{workspace_slug}/projects/{project_id}/issues/{issue_id}/
  method: DELETE
  auth_secret: plane-api-key
  auth_header: X-API-Key
  auth_scheme: api-key
```

## Workflow

1. **First time**: List workspaces to get the `slug`
2. **Then**: List projects to get `project_id`
3. **Then**: Perform issue operations using workspace slug + project ID

## Tips

- Issue descriptions use `description_html` (HTML format) not plain `description`
- State values depend on project configuration — list issues first to see available states
- The API returns paginated results; check `next_page_token` in response for pagination
