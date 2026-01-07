# Setup Instructions

## Environment Configuration

This application uses environment variables for configuration. Sensitive information like API keys are stored in a `.env` file that is **not committed to git**.

### Initial Setup

1. Copy the example environment file:
   ```bash
   cp .env.example .env
   ```

2. Edit `.env` and replace the placeholder values with your actual API keys:
   ```bash
   nano .env  # or use your preferred editor
   ```

3. Update the following values:
   - `JELLYFIN_API_KEY`: Your Jellyfin API key
   - `JELLYFIN_USER_ID`: Your Jellyfin user ID
   - `RADARR_API_KEY`: Your Radarr API key
   - `SONARR_API_KEY`: Your Sonarr API key

### Getting API Keys

**Jellyfin:**
- Go to Dashboard → API Keys
- Create a new API key for this application

**Radarr/Sonarr:**
- Go to Settings → General
- Copy the API Key from the Security section

### Deployment

When you run `./deploy.sh`, it will:
1. Sync all code files to the server
2. Separately sync the `.env` file (which is gitignored)
3. Docker Compose will automatically load variables from `.env`

### Security Notes

- **Never commit `.env` to git** - it's already in `.gitignore`
- The `.env.example` file is safe to commit (it has placeholder values)
- If you suspect your API keys are compromised, regenerate them immediately in Jellyfin/Radarr/Sonarr
- Consider regenerating the API keys that were previously in docker-compose.yml since they were committed to git

### Dry Run Mode

To test deletions without actually removing files:
```bash
# In .env:
DRY_RUN_MODE=true
```

Set to `false` or remove this line to enable actual deletions.
