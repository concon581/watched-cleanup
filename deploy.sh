#!/bin/bash

# Deploy watched-cleanup to minipc server
# Usage: ./deploy.sh

set -e  # Exit on any error

SERVER="minipc"
SERVER_PATH="/docker/appdata/watched-cleanup"
LOCAL_PATH="/Users/connordixon/Developer/watched-cleanup"

echo "🚀 Deploying watched-cleanup to $SERVER..."
echo ""

# Sync files to server (excluding build artifacts and git)
echo "📦 Syncing files..."
rsync -avz --progress \
  --exclude 'watched-cleanup' \
  --exclude '.git' \
  --exclude 'deploy.sh' \
  --exclude '.DS_Store' \
  "$LOCAL_PATH/" \
  "$SERVER:$SERVER_PATH/"

# Ensure .env exists on server
if [ -f "$LOCAL_PATH/.env" ]; then
  echo "📄 Syncing .env file..."
  rsync -avz "$LOCAL_PATH/.env" "$SERVER:$SERVER_PATH/.env"
else
  echo "⚠️  Warning: No .env file found locally. Make sure it exists on the server."
fi

echo ""
echo "🔨 Building and restarting container on server..."
ssh "$SERVER" "cd $SERVER_PATH && docker compose up -d --build"

echo ""
echo "✅ Deployment complete!"
echo ""
echo "📊 Check status with:"
echo "   ssh $SERVER 'docker compose -f $SERVER_PATH/docker-compose.yml ps'"
echo ""
echo "📋 View logs with:"
echo "   ssh $SERVER 'docker compose -f $SERVER_PATH/docker-compose.yml logs -f'"
