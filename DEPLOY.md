# Deployment Guide

The minipc migrated from Docker to **Podman** (July 2026). Docker is stopped
and disabled on the server — don't try to start it.

- **Source / build context:** `/docker/appdata/watched-cleanup` (rsynced from this repo)
- **Running stack (dockge-managed):** `/opt/stacks/watched-cleanup` — `compose.yaml` plus a root-owned `.env` holding the secrets
- The container runs as a **root Podman** container named `watched-cleanup`

## Quick Deploy

After making changes locally:

```bash
./deploy.sh
```

The script will:
1. Sync your local changes to `/docker/appdata/watched-cleanup`
2. Build the image with `podman build`
3. Copy `.env` into the stack dir and `podman compose up -d` the stack

## Useful Commands

### Check if the service is running
```bash
ssh minipc 'sudo podman ps --filter name=watched-cleanup'
```

### View logs
```bash
ssh minipc 'sudo podman logs -f watched-cleanup'
```

### Restart the service
```bash
ssh minipc 'cd /opt/stacks/watched-cleanup && sudo podman compose restart'
```

### Stop the service
```bash
ssh minipc 'cd /opt/stacks/watched-cleanup && sudo podman compose down'
```

### Health check
```bash
curl http://192.168.1.238:6969/healthz
```

## Development Workflow

1. **Make changes locally**
2. **Test locally**:
   ```bash
   go test ./... && go build
   ```
3. **Deploy to server**:
   ```bash
   ./deploy.sh
   ```
4. **Check it's working**:
   - Visit http://192.168.1.238:6969 (basic auth — credentials in `.env`)
   - `ssh minipc 'sudo podman logs watched-cleanup | tail -20'`

## Troubleshooting

### Deploy script won't run
```bash
chmod +x deploy.sh
```

### Can't connect to minipc
Make sure SSH is configured in `~/.ssh/config`:
```
Host minipc
    HostName 192.168.1.238
    User <your-username>
```

### Container won't start
```bash
ssh minipc 'sudo podman logs watched-cleanup'
```

### Stack config
The live compose file is `/opt/stacks/watched-cleanup/compose.yaml`. It reads
all secrets from `/opt/stacks/watched-cleanup/.env` (root-owned, 600), which
`deploy.sh` refreshes from the local `.env` on every deploy.
