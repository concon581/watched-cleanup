# Deployment Guide

## Quick Deploy

After making changes locally:

```bash
./deploy.sh
```

That's it! The script will:
1. Sync your local changes to the minipc server
2. Rebuild and restart the Docker container
3. Show you status and log commands

## Manual Deployment

If you need to deploy manually:

```bash
# Sync files
rsync -avz --exclude 'watched-cleanup' --exclude '.git' \
  /Users/connordixon/Developer/watched-cleanup/ \
  minipc:/docker/appdata/watched-cleanup/

# Rebuild on server
ssh minipc "cd /docker/appdata/watched-cleanup && docker compose up -d --build"
```

## Useful Commands

### Check if the service is running
```bash
ssh minipc 'docker compose -f /docker/appdata/watched-cleanup/docker-compose.yml ps'
```

### View logs
```bash
ssh minipc 'docker compose -f /docker/appdata/watched-cleanup/docker-compose.yml logs -f'
```

### Restart the service
```bash
ssh minipc 'docker compose -f /docker/appdata/watched-cleanup/docker-compose.yml restart'
```

### Stop the service
```bash
ssh minipc 'docker compose -f /docker/appdata/watched-cleanup/docker-compose.yml down'
```

## Development Workflow

1. **Make changes locally** using Claude Code
2. **Test locally** (optional):
   ```bash
   go build
   ./watched-cleanup
   ```
3. **Deploy to server**:
   ```bash
   ./deploy.sh
   ```
4. **Check it's working**:
   - Visit http://nas.home.arpa:6969 (or your server's URL)
   - Check logs if needed

## Troubleshooting

### Deploy script won't run
```bash
chmod +x deploy.sh
```

### Can't connect to minipc
Make sure SSH is configured in `~/.ssh/config`:
```
Host minipc
    HostName <your-server-ip>
    User <your-username>
```

### Docker container won't start
Check logs:
```bash
ssh minipc 'docker compose -f /docker/appdata/watched-cleanup/docker-compose.yml logs'
```
