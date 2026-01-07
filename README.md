# Watched Cleanup

A Go application for managing and cleaning up watched media from Jellyfin, with automatic hardlink detection and integration with Radarr/Sonarr for preventing re-downloads.

## Features

- 🎬 **Jellyfin Integration** - Fetch watched movies and TV shows from Jellyfin
- 🔗 **Hardlink Detection** - Find and delete hardlinks to prevent orphaned torrent files
- 📡 **Radarr/Sonarr Integration** - Automatically unmonitor deleted media to prevent re-downloads
- 🎯 **3-Stage Deletion Pipeline**:
  1. Inode/Hardlink cleanup - Delete hardlinks and original files
  2. Radarr/Sonarr unmonitoring - Prevent re-downloads
  3. Jellyfin library cleanup - Remove from database
- 🧪 **Dry-Run Mode** - Test deletions without actually removing files
- 📊 **Real-time Progress Tracking** - Monitor deletion progress with detailed stage updates
- 🌐 **Web UI** - Easy-to-use interface for browsing and deleting media

## Architecture

The application is organized into focused packages:

```
watched-cleanup/
├── main.go                    (905 lines - HTTP handlers, routing, initialization)
├── models/                    (All type definitions)
│   └── models.go
├── jellyfin/                  (Jellyfin API client)
│   └── client.go
├── radarr/                    (Radarr API client)
│   └── client.go
├── sonarr/                    (Sonarr API client)
│   └── client.go
├── deletion/                  (Delete orchestration logic)
│   └── orchestrator.go
└── filesystem/                (File and hardlink utilities)
    └── utils.go
```

## Requirements

- Go 1.25.5 or later
- Jellyfin server
- Radarr (optional, for movie monitoring)
- Sonarr (optional, for TV series monitoring)
- Docker and Docker Compose (for containerized deployment)

## Configuration

Set the following environment variables:

### Jellyfin (Required)
```bash
JELLYFIN_BASE_URL=http://jellyfin:8096/
JELLYFIN_API_KEY=your_api_key_here
JELLYFIN_USER_ID=your_user_id_here
```

### Radarr (Optional)
```bash
RADARR_BASE_URL=http://radarr:7878/
RADARR_API_KEY=your_radarr_api_key
```

### Sonarr (Optional)
```bash
SONARR_BASE_URL=http://sonarr:8989/
SONARR_API_KEY=your_sonarr_api_key
```

### Other Settings
```bash
TORRENTS_PATH=/data/torrents        # Directory to search for hardlinks
DRY_RUN_MODE=false                  # Set to 'true' to enable dry-run mode globally
```

## Installation

### Docker (Recommended)

1. Clone the repository
2. Copy `.env.example` to `.env` and configure your settings
3. Run with Docker Compose:

```bash
docker-compose up -d
```

The application will be available at `http://localhost:6969`

### Manual Build

1. Clone the repository
2. Install dependencies:

```bash
go mod download
```

3. Build the application:

```bash
go build -o watched-cleanup
```

4. Run:

```bash
./watched-cleanup
```

## Usage

### Web Interface

1. Navigate to `http://localhost:6969`
2. View watched movies at `/`
3. View watched TV shows at `/tv`
4. Click "Refresh" to fetch latest data from Jellyfin
5. Select items to delete and click "Delete Selected"
6. Monitor deletion progress in real-time

### API Endpoints

#### Data Refresh
- `GET /refresh` - Refresh movie data
- `GET /refresh-tv` - Refresh TV data
- `GET /refresh-status` - Get refresh progress

#### Deletion
- `POST /delete-preview` - Preview items to be deleted
- `POST /delete-confirm` - Confirm and start deletion
- `GET /delete-progress` - Get deletion progress
- `POST /delete` - Legacy deletion endpoint

#### Test Endpoints
- `GET /test/radarr/movies` - List all Radarr movies
- `GET /test/radarr/search-path?path=<filepath>` - Search Radarr by file path
- `GET /test/radarr/search-title?title=<title>&year=<year>` - Search Radarr by title
- `GET /test/sonarr/series` - List all Sonarr series
- `GET /test/sonarr/search-path?path=<filepath>` - Search Sonarr by file path
- `GET /test/sonarr/search-title?title=<title>` - Search Sonarr by title

## Development

### Project Structure

- **models/** - Data structures and type definitions
- **jellyfin/** - Jellyfin API client with parallel data fetching
- **radarr/** - Radarr API client for movie management
- **sonarr/** - Sonarr API client for TV series management
- **deletion/** - Deletion orchestration with 3-stage pipeline
- **filesystem/** - File utilities including hardlink detection
- **main.go** - HTTP handlers, routing, and server initialization
- **templates/** - HTML templates for web UI

### Building

```bash
# Build for current platform
go build -o watched-cleanup

# Build for Linux (for Docker)
GOOS=linux GOARCH=amd64 go build -o watched-cleanup

# Run tests
go test ./...
```

## Safety Features

1. **Dry-Run Mode** - Test deletions without actually removing files
2. **Global Dry-Run** - Set `DRY_RUN_MODE=true` to force all deletions into test mode
3. **Progress Tracking** - Detailed progress with stage-level error reporting
4. **Mutex Protection** - Thread-safe operations with proper locking
5. **Error Collection** - All errors are logged and reported to the user
6. **HTTP Timeouts** - 30-second timeouts on all external API calls

## Error Handling

The application includes comprehensive error handling:

- Division by zero protection in progress calculations
- HTTP request timeouts (30s) with context cancellation
- Proper error logging for all external API calls
- Response body cleanup to prevent resource leaks
- Detailed error reporting in deletion results

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is provided as-is for personal use.

## Changelog

### v1.0.2
- Improved error handling and HTTP timeouts
- Added global dry-run mode via environment variable
- Enhanced progress tracking with stage-level updates
- Fixed division by zero in progress calculations

### v1.0.1
- Refactored codebase into modular packages
- Improved code organization and maintainability
- Added comprehensive error logging
- Enhanced deletion pipeline with 3-stage process

### v1.0.0
- Initial release
- Basic Jellyfin integration
- Hardlink detection
- Radarr/Sonarr integration
