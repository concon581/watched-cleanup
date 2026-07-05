#!/bin/bash

# Deploy watched-cleanup to the minipc server (Podman + dockge)
#
# The server migrated from Docker to Podman (July 2026). The source/build
# context lives at /docker/appdata/watched-cleanup; the running stack is
# dockge-managed at /opt/stacks/watched-cleanup (compose.yaml + .env).
#
# Usage: ./deploy.sh

set -e  # Exit on any error

SERVER="minipc"
BUILD_PATH="/docker/appdata/watched-cleanup"
STACK_PATH="/opt/stacks/watched-cleanup"
LOCAL_PATH="$(cd "$(dirname "$0")" && pwd)"

echo "🚀 Deploying watched-cleanup to $SERVER..."
echo ""

# Sync files to server (excluding build artifacts and git)
echo "📦 Syncing source..."
rsync -avz --progress \
  --exclude 'watched-cleanup' \
  --exclude '.git' \
  --exclude '.claude' \
  --exclude '.codex' \
  --exclude 'deploy.sh' \
  --exclude '.DS_Store' \
  "$LOCAL_PATH/" \
  "$SERVER:$BUILD_PATH/"

# Ensure .env exists on server
if [ -f "$LOCAL_PATH/.env" ]; then
  echo "📄 Syncing .env file..."
  rsync -avz "$LOCAL_PATH/.env" "$SERVER:$BUILD_PATH/.env"
else
  echo "⚠️  Warning: No .env file found locally. Make sure it exists on the server."
fi

echo ""
echo "🔨 Building image and restarting stack on server..."
ssh "$SERVER" "
  set -e
  rm -rf $BUILD_PATH/.claude $BUILD_PATH/.codex $BUILD_PATH/.DS_Store
  sudo podman build -t watched-cleanup-watched-cleanup:latest $BUILD_PATH
  sudo cp $BUILD_PATH/.env $STACK_PATH/.env
  sudo chmod 600 $STACK_PATH/.env
  sudo chown root:root $STACK_PATH/.env
  cd $STACK_PATH && sudo podman compose up -d
"

echo ""
echo "✅ Deployment complete!"
echo ""
echo "📊 Check status with:"
echo "   ssh $SERVER 'sudo podman ps --filter name=watched-cleanup'"
echo ""
echo "📋 View logs with:"
echo "   ssh $SERVER 'sudo podman logs -f watched-cleanup'"
