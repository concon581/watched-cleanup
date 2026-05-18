package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/concon581/watched-cleanup/deletion"
	"github.com/concon581/watched-cleanup/filesystem"
	"github.com/concon581/watched-cleanup/jellyfin"
	"github.com/concon581/watched-cleanup/models"
	"github.com/concon581/watched-cleanup/radarr"
	"github.com/concon581/watched-cleanup/sonarr"
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

var (
	cachedMovies    models.MovieList
	cachedSeries    []models.Series
	isRefreshing    bool
	lastRefresh     time.Time
	cacheMutex      sync.RWMutex
	refreshProgress models.RefreshProgress
	progressMutex   sync.RWMutex

	// Delete progress tracking
	deleteProgress models.DeleteProgress
	deleteResult   models.DeleteResult
	deleteMutex    sync.RWMutex
	isDeleting     bool

	// Orphan scan cache
	orphanCache    models.OrphanScanResponse
	orphanScanning bool
	orphanMutex    sync.RWMutex

	// Shared scan cache/state used by startup, refresh, storage, and orphan views.
	storageDataCache models.StorageDataResponse
	storageDataReady bool
	scanStatus       models.ScanStatus
	scanMutex        sync.RWMutex
)

// Load templates from files
var tmpl *template.Template
var tvTmpl *template.Template
var deletePreviewTmpl *template.Template
var deleteProgressTmpl *template.Template
var deleteSummaryTmpl *template.Template
var storageTmpl *template.Template
var storageDashboardTmpl *template.Template

func initTemplates() {
	var err error
	funcMap := template.FuncMap{
		"formatTime": func(t time.Time) string {
			return t.Format("Jan 2, 2006 3:04 PM")
		},
		"add": func(a, b float64) float64 {
			return a + b
		},
		"daysAgo": func(t time.Time) int {
			return int(time.Since(t).Hours() / 24)
		},
	}

	// Try to find templates relative to executable or current directory
	templateDir := "templates"
	if _, err := os.Stat(templateDir); os.IsNotExist(err) {
		// Try relative to executable
		exe, err := os.Executable()
		if err == nil {
			exeDir := filepath.Dir(exe)
			possibleDir := filepath.Join(exeDir, "templates")
			if _, err := os.Stat(possibleDir); err == nil {
				templateDir = possibleDir
			}
		}
	}

	tmpl, err = template.New("base.html").Funcs(funcMap).ParseFiles(
		filepath.Join(templateDir, "base.html"),
		filepath.Join(templateDir, "movies.html"),
	)
	if err != nil {
		panic(fmt.Sprintf("Error loading movies template: %v", err))
	}

	tvTmpl, err = template.New("base.html").Funcs(funcMap).ParseFiles(
		filepath.Join(templateDir, "base.html"),
		filepath.Join(templateDir, "tv.html"),
	)
	if err != nil {
		panic(fmt.Sprintf("Error loading TV template: %v", err))
	}

	storageTmpl, err = template.New("base.html").Funcs(funcMap).ParseFiles(
		filepath.Join(templateDir, "base.html"),
		filepath.Join(templateDir, "storage.html"),
	)
	if err != nil {
		panic(fmt.Sprintf("Error loading storage template: %v", err))
	}

	storageDashboardTmpl, err = template.New("base.html").Funcs(funcMap).ParseFiles(
		filepath.Join(templateDir, "base.html"),
		filepath.Join(templateDir, "storage-dashboard.html"),
	)
	if err != nil {
		panic(fmt.Sprintf("Error loading storage-dashboard template: %v", err))
	}

	deletePreviewTmpl, err = template.ParseFiles(filepath.Join(templateDir, "delete-preview.html"))
	if err != nil {
		panic(fmt.Sprintf("Error loading delete preview template: %v", err))
	}

	deleteProgressTmpl, err = template.ParseFiles(filepath.Join(templateDir, "delete-progress.html"))
	if err != nil {
		panic(fmt.Sprintf("Error loading delete progress template: %v", err))
	}

	deleteSummaryTmpl, err = template.ParseFiles(filepath.Join(templateDir, "delete-summary.html"))
	if err != nil {
		panic(fmt.Sprintf("Error loading delete summary template: %v", err))
	}
}

// loadEnvFile loads environment variables from .env file if it exists
// This is for local development - Docker uses docker-compose.yml
func loadEnvFile() {
	envFile := ".env"
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		return // .env file doesn't exist, skip
	}

	file, err := os.Open(envFile)
	if err != nil {
		fmt.Printf("watched-cleanup: Warning - could not open .env file: %v\n", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE format
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			value = strings.Trim(value, "\"'")

			// Only set if not already set (allows override via actual env vars)
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
}

func main() {
	// Load .env file for local development (Docker uses docker-compose.yml)
	loadEnvFile()

	initTemplates()

	http.HandleFunc("/", handleMovies)
	http.HandleFunc("/tv", handleTV)
	http.HandleFunc("/storage", handleStorage)
	http.HandleFunc("/storage-dashboard", handleStorageDashboard)
	http.HandleFunc("/api/storage-data", handleStorageAPI)
	http.HandleFunc("/api/scan-status", handleScanStatusAPI)
	http.HandleFunc("/api/orphan-data", handleOrphanData)
	http.HandleFunc("/api/orphan-scan", handleOrphanScan)
	http.HandleFunc("/api/orphan-delete", handleOrphanDelete)
	http.HandleFunc("/refresh", handleRefreshMovies)
	http.HandleFunc("/refresh-tv", handleRefreshTV)
	http.HandleFunc("/refresh-all", handleRefreshAll)
	http.HandleFunc("/refresh-status", handleRefreshStatus)
	http.HandleFunc("/delete-preview", handleDeletePreview)
	http.HandleFunc("/delete-confirm", handleDeleteConfirm)
	http.HandleFunc("/delete-progress", handleDeleteProgress)
	http.HandleFunc("/delete", handleDelete) // Keep for backwards compatibility

	// Test endpoints for Radarr/Sonarr API
	http.HandleFunc("/test/radarr/movies", handleTestRadarrMovies)
	http.HandleFunc("/test/radarr/search-path", handleTestRadarrSearchPath)
	http.HandleFunc("/test/radarr/search-title", handleTestRadarrSearchTitle)
	http.HandleFunc("/test/sonarr/series", handleTestSonarrSeries)
	http.HandleFunc("/test/sonarr/search-path", handleTestSonarrSearchPath)
	http.HandleFunc("/test/sonarr/search-title", handleTestSonarrSearchTitle)

	fmt.Println("watched-cleanup v1.0.2 - hardlink test starting...")
	fmt.Println("Server starting on :6969")
	fmt.Println("Test endpoints available:")
	fmt.Println("  GET /test/radarr/movies - List all Radarr movies")
	fmt.Println("  GET /test/radarr/search-path?path=<filepath> - Search Radarr by file path")
	fmt.Println("  GET /test/radarr/search-title?title=<title>&year=<year> - Search Radarr by title and year")
	fmt.Println("  GET /test/sonarr/series - List all Sonarr series")
	fmt.Println("  GET /test/sonarr/search-path?path=<filepath> - Search Sonarr by file path")
	fmt.Println("  GET /test/sonarr/search-title?title=<title> - Search Sonarr by title")
	startUnifiedScan("startup")
	http.ListenAndServe(":6969", nil)

}

func updateProgress(current, total int, message string) {
	progressMutex.Lock()
	refreshProgress = models.RefreshProgress{
		Current: current,
		Total:   total,
		Message: message,
	}
	progressMutex.Unlock()
}

func defaultScanStages() []models.ScanStageStatus {
	return []models.ScanStageStatus{
		{Name: "Jellyfin movies", Status: "pending", Message: "Waiting"},
		{Name: "Jellyfin TV", Status: "pending", Message: "Waiting"},
		{Name: "Disk breakdown", Status: "pending", Message: "Waiting"},
		{Name: "Hardlink orphan check", Status: "pending", Message: "Waiting"},
	}
}

func scanStatusSnapshot() models.ScanStatus {
	scanMutex.RLock()
	defer scanMutex.RUnlock()
	status := scanStatus
	status.Stages = append([]models.ScanStageStatus(nil), scanStatus.Stages...)
	return status
}

func setScanStage(name, status, message string) {
	now := time.Now()
	scanMutex.Lock()
	scanStatus.Message = message
	for i := range scanStatus.Stages {
		if scanStatus.Stages[i].Name != name {
			continue
		}
		scanStatus.Stages[i].Status = status
		scanStatus.Stages[i].Message = message
		if status == "running" && scanStatus.Stages[i].StartedAt.IsZero() {
			scanStatus.Stages[i].StartedAt = now
		}
		if status == "complete" || status == "error" {
			scanStatus.Stages[i].FinishedAt = now
		}
		break
	}
	scanMutex.Unlock()
	updateProgress(0, 0, message)
}

func startUnifiedScan(reason string) bool {
	scanMutex.Lock()
	if scanStatus.Scanning {
		scanMutex.Unlock()
		return false
	}
	now := time.Now()
	scanStatus = models.ScanStatus{
		Scanning:  true,
		Reason:    reason,
		Message:   "Starting scan...",
		StartedAt: now,
		Stages:    defaultScanStages(),
	}
	scanMutex.Unlock()

	go runUnifiedScan(reason)
	return true
}

func finishUnifiedScan(data models.StorageDataResponse, err error) {
	now := time.Now()
	scanMutex.Lock()
	if err != nil {
		scanStatus.Message = "Scan failed: " + err.Error()
		scanStatus.Error = err.Error()
	} else {
		scanStatus.Message = "Scan complete"
		scanStatus.Error = ""
		scanStatus.ScannedAt = now
	}
	scanStatus.Scanning = false
	scanStatus.FinishedAt = now
	finalStatus := scanStatus
	finalStatus.Stages = append([]models.ScanStageStatus(nil), scanStatus.Stages...)
	if err == nil {
		data.Scan = finalStatus
		storageDataCache = data
		storageDataReady = true
	}
	scanMutex.Unlock()
}

func emptyStorageData() models.StorageDataResponse {
	return models.StorageDataResponse{
		Movies: models.StorageCategoryData{
			Items: []models.StorageItem{},
		},
		TV: models.StorageCategoryData{
			Items: []models.StorageItem{},
		},
		SizeDistribution: models.SizeDistribution{
			Movies: map[string]int{},
			TV:     map[string]int{},
		},
		Other: models.StorageOtherBreakdown{
			Items:     []models.StorageDiskEntry{},
			Libraries: []models.StorageLibraryOnDisk{},
		},
		Timestamp: time.Now(),
	}
}

func runUnifiedScan(reason string) {
	fmt.Printf("watched-cleanup: Starting unified scan (%s)\n", reason)

	cacheMutex.Lock()
	isRefreshing = true
	cacheMutex.Unlock()
	defer func() {
		cacheMutex.Lock()
		isRefreshing = false
		cacheMutex.Unlock()
	}()

	setScanStage("Jellyfin movies", "running", "Fetching Jellyfin movies...")
	newMovies := jellyfin.FetchMovieData(httpClient, updateProgress)
	cacheMutex.Lock()
	cachedMovies = newMovies
	cacheMutex.Unlock()
	setScanStage("Jellyfin movies", "complete", fmt.Sprintf("Loaded %d movies", len(newMovies.Items)))

	setScanStage("Jellyfin TV", "running", "Fetching Jellyfin TV...")
	newSeries := jellyfin.FetchTVData(httpClient, updateProgress)
	cacheMutex.Lock()
	cachedSeries = newSeries
	lastRefresh = time.Now()
	cacheMutex.Unlock()
	setScanStage("Jellyfin TV", "complete", fmt.Sprintf("Loaded %d series", len(newSeries)))

	setScanStage("Disk breakdown", "running", "Measuring storage folders...")
	data := getStorageData()
	setScanStage("Disk breakdown", "complete", "Storage breakdown updated")

	setScanStage("Hardlink orphan check", "running", "Checking hardlinks between torrents and libraries...")
	orphanData, err := collectOrphanScan()
	if err != nil {
		setScanStage("Hardlink orphan check", "error", "Hardlink orphan scan failed")
		orphanMutex.Lock()
		orphanScanning = false
		orphanCache = orphanData
		orphanMutex.Unlock()
		finishUnifiedScan(data, err)
		return
	}
	orphanMutex.Lock()
	orphanScanning = false
	orphanCache = orphanData
	orphanMutex.Unlock()
	setScanStage("Hardlink orphan check", "complete", "Hardlink orphan scan updated")

	finishUnifiedScan(data, nil)
	fmt.Println("watched-cleanup: Unified scan complete")
}

func handleMovies(w http.ResponseWriter, r *http.Request) {
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()

	data := struct {
		Items           []models.Movie
		LastRefresh     time.Time
		JellyfinBaseURL string
	}{
		Items:           cachedMovies.Items,
		LastRefresh:     lastRefresh,
		JellyfinBaseURL: os.Getenv("JELLYFIN_BASE_URL"),
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		fmt.Printf("Template error: %v\n", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func handleTV(w http.ResponseWriter, r *http.Request) {
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()

	// Calculate if each series is fully watched
	type EnhancedSeries struct {
		models.Series
		FullyWatched bool
	}

	enhancedSeries := make([]EnhancedSeries, len(cachedSeries))
	for i, s := range cachedSeries {
		fullyWatched := true
		for _, season := range s.Seasons {
			if season.WatchedCount < season.TotalCount {
				fullyWatched = false
				break
			}
		}
		enhancedSeries[i] = EnhancedSeries{
			Series:       s,
			FullyWatched: fullyWatched,
		}
	}

	data := struct {
		Series          []EnhancedSeries
		LastRefresh     time.Time
		JellyfinBaseURL string
	}{
		Series:          enhancedSeries,
		LastRefresh:     lastRefresh,
		JellyfinBaseURL: os.Getenv("JELLYFIN_BASE_URL"),
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tvTmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		fmt.Printf("Template error: %v\n", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func handleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if !startUnifiedScan("manual refresh") {
		w.Write([]byte("Already refreshing"))
		return
	}
	w.Write([]byte("Refresh started"))
}

// getStorageData calculates and returns all storage information
func getStorageData() models.StorageDataResponse {
	cacheMutex.RLock()
	defer cacheMutex.RUnlock()

	// Calculate totals
	var totalMovies float64
	var moviesWatched, moviesUnwatched int
	moviesItems := make([]models.StorageItem, 0, len(cachedMovies.Items))

	for _, m := range cachedMovies.Items {
		totalMovies += m.SizeGB
		item := models.StorageItem{
			Name:    m.Name,
			SizeGB:  m.SizeGB,
			Watched: m.IsPlayed,
		}
		moviesItems = append(moviesItems, item)
		if m.IsPlayed {
			moviesWatched++
		} else {
			moviesUnwatched++
		}
	}

	var totalTV float64
	var tvWatched, tvUnwatched int
	tvItems := make([]models.StorageItem, 0, len(cachedSeries))

	for _, s := range cachedSeries {
		totalTV += s.TotalSize
		seasons := make([]models.StorageSeason, len(s.Seasons))
		for i, season := range s.Seasons {
			seasons[i] = models.StorageSeason{
				Number: season.SeasonNumber,
				SizeGB: season.SizeGB,
			}
		}
		// Series is marked watched if all seasons are fully played
		isWatched := len(s.Seasons) > 0
		for _, season := range s.Seasons {
			if season.WatchedCount < season.TotalCount {
				isWatched = false
				break
			}
		}
		item := models.StorageItem{
			Name:    s.Name,
			SizeGB:  s.TotalSize,
			Watched: isWatched,
			Seasons: seasons,
		}
		tvItems = append(tvItems, item)
		if isWatched {
			tvWatched++
		} else {
			tvUnwatched++
		}
	}

	// Filesystem stats
	dataPath := os.Getenv("DATA_PATH")
	if dataPath == "" {
		dataPath = "/data"
	}

	var stat syscall.Statfs_t
	var fsStats models.FilesystemStats
	if err := syscall.Statfs(dataPath, &stat); err == nil {
		totalBytes := uint64(stat.Blocks) * uint64(stat.Bsize)
		freeBytes := uint64(stat.Bavail) * uint64(stat.Bsize)
		usedBytes := totalBytes - freeBytes
		fsStats.TotalGB = float64(totalBytes) / (1024.0 * 1024.0 * 1024.0)
		fsStats.FreeGB = float64(freeBytes) / (1024.0 * 1024.0 * 1024.0)
		fsStats.UsedGB = float64(usedBytes) / (1024.0 * 1024.0 * 1024.0)
		if totalBytes > 0 {
			fsStats.UsedPct = (float64(usedBytes) / float64(totalBytes)) * 100.0
		}
	}

	// Size distribution buckets
	sizeDistribution := models.SizeDistribution{
		Movies: make(map[string]int),
		TV:     make(map[string]int),
	}
	sizeDistribution.Movies["0-500MB"] = 0
	sizeDistribution.Movies["500MB-2GB"] = 0
	sizeDistribution.Movies["2GB-10GB"] = 0
	sizeDistribution.Movies["10GB+"] = 0
	sizeDistribution.TV["0-500MB"] = 0
	sizeDistribution.TV["500MB-2GB"] = 0
	sizeDistribution.TV["2GB-10GB"] = 0
	sizeDistribution.TV["10GB+"] = 0

	// Categorize movies by size
	for _, item := range moviesItems {
		sizeGB := item.SizeGB
		if sizeGB < 0.5 {
			sizeDistribution.Movies["0-500MB"]++
		} else if sizeGB < 2 {
			sizeDistribution.Movies["500MB-2GB"]++
		} else if sizeGB < 10 {
			sizeDistribution.Movies["2GB-10GB"]++
		} else {
			sizeDistribution.Movies["10GB+"]++
		}
	}

	// Categorize TV by size
	for _, item := range tvItems {
		sizeGB := item.SizeGB
		if sizeGB < 0.5 {
			sizeDistribution.TV["0-500MB"]++
		} else if sizeGB < 2 {
			sizeDistribution.TV["500MB-2GB"]++
		} else if sizeGB < 10 {
			sizeDistribution.TV["2GB-10GB"]++
		} else {
			sizeDistribution.TV["10GB+"]++
		}
	}

	otherBreakdown := buildOtherBreakdown(dataPath, totalMovies, totalTV, fsStats.UsedGB)

	return models.StorageDataResponse{
		Filesystem: fsStats,
		Movies: models.StorageCategoryData{
			TotalGB:   totalMovies,
			Count:     len(cachedMovies.Items),
			Items:     moviesItems,
			Watched:   moviesWatched,
			Unwatched: moviesUnwatched,
		},
		TV: models.StorageCategoryData{
			TotalGB:   totalTV,
			Count:     len(cachedSeries),
			Items:     tvItems,
			Watched:   tvWatched,
			Unwatched: tvUnwatched,
		},
		SizeDistribution: sizeDistribution,
		Other:            otherBreakdown,
		Timestamp:        time.Now(),
	}
}

func buildOtherBreakdown(dataPath string, moviesGB, tvGB, usedGB float64) models.StorageOtherBreakdown {
	torrentsPath := os.Getenv("TORRENTS_PATH")
	if torrentsPath == "" {
		torrentsPath = filepath.Join(dataPath, "torrents")
	}

	otherTotal := usedGB - moviesGB - tvGB
	if otherTotal < 0 {
		otherTotal = 0
	}

	result := models.StorageOtherBreakdown{
		TotalGB: otherTotal,
		Items:   []models.StorageDiskEntry{},
	}

	scanned, err := filesystem.ScanStorageBreakdown(dataPath, torrentsPath)
	if err != nil {
		return result
	}

	var movieLibs, tvLibs []models.StorageLibraryOnDisk
	for _, entry := range scanned {
		if entry.Category == "movies" {
			movieLibs = append(movieLibs, models.StorageLibraryOnDisk{
				Name: entry.Name, Path: entry.Path, Category: entry.Category, DiskGB: entry.SizeGB,
			})
			continue
		}
		if entry.Category == "tv" {
			tvLibs = append(tvLibs, models.StorageLibraryOnDisk{
				Name: entry.Name, Path: entry.Path, Category: entry.Category, DiskGB: entry.SizeGB,
			})
			continue
		}

		result.Items = append(result.Items, models.StorageDiskEntry{
			Name:     entry.Name,
			Path:     entry.Path,
			SizeGB:   entry.SizeGB,
			Category: entry.Category,
		})
		result.BeyondMediaGB += entry.SizeGB
	}

	result.Libraries = append(result.Libraries, attachJellyfinTotals(movieLibs, moviesGB)...)
	result.Libraries = append(result.Libraries, attachJellyfinTotals(tvLibs, tvGB)...)

	sort.Slice(result.Libraries, func(i, j int) bool {
		return result.Libraries[i].DiskGB > result.Libraries[j].DiskGB
	})
	sort.Slice(result.Items, func(i, j int) bool {
		return result.Items[i].SizeGB > result.Items[j].SizeGB
	})

	if otherTotal > result.BeyondMediaGB {
		result.GapGB = otherTotal - result.BeyondMediaGB
	}

	return result
}

func resolveLibraryPaths(dataPath, torrentsPath string) []string {
	if v := os.Getenv("LIBRARY_PATHS"); v != "" {
		var paths []string
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				paths = append(paths, part)
			}
		}
		if len(paths) > 0 {
			return paths
		}
	}

	scanned, err := filesystem.ScanStorageBreakdown(dataPath, torrentsPath)
	if err == nil {
		var paths []string
		for _, entry := range scanned {
			if entry.Category == "movies" || entry.Category == "tv" {
				paths = append(paths, filepath.Join(dataPath, entry.Path))
			}
		}
		if len(paths) > 0 {
			return paths
		}
	}

	candidates := []string{"media/movies", "media/tv", "movies", "tv", "Shows", "Movies"}
	var paths []string
	for _, rel := range candidates {
		full := filepath.Join(dataPath, rel)
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			paths = append(paths, full)
		}
	}
	return paths
}

func collectOrphanScan() (models.OrphanScanResponse, error) {
	dataPath := os.Getenv("DATA_PATH")
	if dataPath == "" {
		dataPath = "/data"
	}
	torrentsPath := os.Getenv("TORRENTS_PATH")
	if torrentsPath == "" {
		torrentsPath = filepath.Join(dataPath, "torrents")
	}
	libraryPaths := resolveLibraryPaths(dataPath, torrentsPath)

	raw, err := filesystem.ScanOrphans(torrentsPath, libraryPaths)
	if err != nil {
		return models.OrphanScanResponse{
			Scanning:     false,
			Message:      "Scan failed: " + err.Error(),
			TorrentsPath: torrentsPath,
			LibraryPaths: libraryPaths,
		}, err
	}

	resp := models.OrphanScanResponse{
		Scanning:     false,
		Message:      "Scan complete",
		ScannedAt:    time.Now(),
		TorrentsPath: raw.TorrentsPath,
		LibraryPaths: raw.LibraryPaths,
	}
	resp.TorrentOrphans = enrichOrphanEntries(raw.TorrentOrphans, "torrents")
	resp.LibraryOrphans = enrichOrphanEntries(raw.LibraryOrphans, "library")
	for _, f := range resp.TorrentOrphans {
		resp.TorrentOrphansGB += f.SizeGB
	}
	for _, f := range resp.LibraryOrphans {
		resp.LibraryOrphansGB += f.SizeGB
	}
	return resp, nil
}

func runOrphanScan() {
	orphanMutex.Lock()
	orphanScanning = true
	orphanCache.Scanning = true
	orphanCache.Message = "Walking torrents and library folders (inode scan)..."
	orphanMutex.Unlock()

	resp, err := collectOrphanScan()
	if err != nil {
		orphanMutex.Lock()
		orphanScanning = false
		orphanCache = resp
		orphanMutex.Unlock()
		return
	}

	orphanMutex.Lock()
	orphanScanning = false
	orphanCache = resp
	orphanMutex.Unlock()
}

func enrichOrphanEntries(files []filesystem.OrphanFile, zone string) []models.OrphanFileEntry {
	pathToMovie := make(map[string]string)
	cacheMutex.RLock()
	for _, m := range cachedMovies.Items {
		if m.Path != "" {
			pathToMovie[filepath.Clean(m.Path)] = m.Name
		}
	}
	cacheMutex.RUnlock()

	out := make([]models.OrphanFileEntry, 0, len(files))
	for _, f := range files {
		entry := models.OrphanFileEntry{
			Path:   f.Path,
			SizeGB: f.SizeGB,
			NLink:  f.NLink,
			Inode:  f.Inode,
		}
		clean := filepath.Clean(f.Path)
		if name, ok := pathToMovie[clean]; ok {
			entry.Label = name
			entry.MediaType = "movie"
		} else if zone == "torrents" {
			entry.MediaType = "torrents"
		} else {
			entry.MediaType = "library"
		}
		out = append(out, entry)
	}
	return out
}

func handleOrphanData(w http.ResponseWriter, r *http.Request) {
	orphanMutex.RLock()
	data := orphanCache
	scanning := orphanScanning
	orphanMutex.RUnlock()

	status := scanStatusSnapshot()
	if scanning || status.Scanning {
		data.Scanning = true
		if status.Message != "" {
			data.Message = status.Message
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleOrphanScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	orphanMutex.Lock()
	status := scanStatusSnapshot()
	if orphanScanning || status.Scanning {
		orphanMutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"scanning": true,
			"message":  "Scan already in progress",
		})
		return
	}
	orphanMutex.Unlock()

	orphanMutex.Lock()
	orphanScanning = true
	orphanCache.Scanning = true
	orphanCache.Message = "Queued as part of unified storage scan..."
	orphanMutex.Unlock()
	startUnifiedScan("orphan scan")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"scanning": true,
		"message":  "Unified storage scan started",
	})
}

type orphanDeleteRequest struct {
	Zone   string   `json:"zone"`
	Paths  []string `json:"paths"`
	DryRun bool     `json:"dryRun"`
}

type orphanDeleteFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type orphanDeleteResponse struct {
	DryRun    bool                     `json:"dryRun"`
	Zone      string                   `json:"zone"`
	Message   string                   `json:"message"`
	Deleted   []models.OrphanFileEntry `json:"deleted"`
	Failed    []orphanDeleteFailure    `json:"failed"`
	DeletedGB float64                  `json:"deletedGB"`
}

func normalizeOrphanZone(zone string) string {
	switch strings.ToLower(strings.TrimSpace(zone)) {
	case "torrent", "torrents":
		return "torrents"
	case "library", "libraries", "media":
		return "library"
	default:
		return ""
	}
}

func cachedOrphanEntries(zone string) map[string]models.OrphanFileEntry {
	orphanMutex.RLock()
	defer orphanMutex.RUnlock()

	var source []models.OrphanFileEntry
	if zone == "torrents" {
		source = orphanCache.TorrentOrphans
	} else if zone == "library" {
		source = orphanCache.LibraryOrphans
	}

	entries := make(map[string]models.OrphanFileEntry, len(source))
	for _, entry := range source {
		entries[filepath.Clean(entry.Path)] = entry
	}
	return entries
}

func removeCachedOrphans(zone string, removed map[string]bool) {
	if len(removed) == 0 {
		return
	}

	orphanMutex.Lock()
	defer orphanMutex.Unlock()

	filter := func(entries []models.OrphanFileEntry) ([]models.OrphanFileEntry, float64) {
		next := make([]models.OrphanFileEntry, 0, len(entries))
		var total float64
		for _, entry := range entries {
			if removed[filepath.Clean(entry.Path)] {
				continue
			}
			next = append(next, entry)
			total += entry.SizeGB
		}
		return next, total
	}

	if zone == "torrents" {
		orphanCache.TorrentOrphans, orphanCache.TorrentOrphansGB = filter(orphanCache.TorrentOrphans)
	} else if zone == "library" {
		orphanCache.LibraryOrphans, orphanCache.LibraryOrphansGB = filter(orphanCache.LibraryOrphans)
	}
	orphanCache.ScannedAt = time.Now()
	orphanCache.Message = "Deleted selected orphan files"
}

func handleOrphanDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	status := scanStatusSnapshot()
	if status.Scanning {
		http.Error(w, "Scan is currently running; try again when it finishes", http.StatusConflict)
		return
	}

	var req orphanDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	zone := normalizeOrphanZone(req.Zone)
	if zone == "" {
		http.Error(w, "Invalid orphan zone", http.StatusBadRequest)
		return
	}
	if len(req.Paths) == 0 {
		http.Error(w, "No paths selected", http.StatusBadRequest)
		return
	}

	allowed := cachedOrphanEntries(zone)
	resp := orphanDeleteResponse{
		DryRun: req.DryRun,
		Zone:   zone,
	}
	removed := make(map[string]bool)

	for _, rawPath := range req.Paths {
		cleanPath := filepath.Clean(rawPath)
		entry, ok := allowed[cleanPath]
		if !ok {
			resp.Failed = append(resp.Failed, orphanDeleteFailure{
				Path:  rawPath,
				Error: "path is not in the current orphan scan",
			})
			continue
		}

		if !req.DryRun {
			info, err := os.Lstat(cleanPath)
			if err != nil {
				resp.Failed = append(resp.Failed, orphanDeleteFailure{Path: cleanPath, Error: err.Error()})
				continue
			}
			if info.IsDir() {
				resp.Failed = append(resp.Failed, orphanDeleteFailure{Path: cleanPath, Error: "directories cannot be deleted here"})
				continue
			}
			if err := os.Remove(cleanPath); err != nil {
				resp.Failed = append(resp.Failed, orphanDeleteFailure{Path: cleanPath, Error: err.Error()})
				continue
			}
			removed[cleanPath] = true
		}

		resp.Deleted = append(resp.Deleted, entry)
		resp.DeletedGB += entry.SizeGB
	}

	if !req.DryRun {
		removeCachedOrphans(zone, removed)
		if len(removed) > 0 {
			startUnifiedScan("orphan delete")
		}
		resp.Message = fmt.Sprintf("Deleted %d orphan file(s)", len(resp.Deleted))
	} else {
		resp.Message = fmt.Sprintf("Tested %d orphan file(s); no files deleted", len(resp.Deleted))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func attachJellyfinTotals(libs []models.StorageLibraryOnDisk, jellyfinGB float64) []models.StorageLibraryOnDisk {
	if len(libs) == 0 {
		return libs
	}
	sort.Slice(libs, func(i, j int) bool {
		return libs[i].DiskGB > libs[j].DiskGB
	})
	libs[0].JellyfinGB = jellyfinGB
	libs[0].DeltaGB = libs[0].DiskGB - jellyfinGB
	return libs
}

// handleStorageAPI returns storage data as JSON for the dashboard
func handleStorageAPI(w http.ResponseWriter, r *http.Request) {
	scanMutex.RLock()
	data := storageDataCache
	ready := storageDataReady
	status := scanStatus
	status.Stages = append([]models.ScanStageStatus(nil), scanStatus.Stages...)
	scanMutex.RUnlock()

	if !ready {
		startUnifiedScan("storage api")
		data = emptyStorageData()
	}
	data.Scan = status

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleScanStatusAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scanStatusSnapshot())
}

// handleStorageDashboard renders the interactive storage dashboard
func handleStorageDashboard(w http.ResponseWriter, r *http.Request) {
	cacheMutex.RLock()
	refreshing := isRefreshing
	lastRefreshTime := lastRefresh
	cacheMutex.RUnlock()

	progressMutex.RLock()
	refreshMessage := refreshProgress.Message
	progressMutex.RUnlock()

	data := struct {
		IsRefreshing  bool
		RefreshStatus string
		LastRefresh   time.Time
	}{
		IsRefreshing:  refreshing,
		RefreshStatus: refreshMessage,
		LastRefresh:   lastRefreshTime,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := storageDashboardTmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		fmt.Printf("Template error: %v\n", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func handleStorage(w http.ResponseWriter, r *http.Request) {
	cacheMutex.RLock()
	refreshing := isRefreshing
	lastRefreshTime := lastRefresh
	cacheMutex.RUnlock()

	progressMutex.RLock()
	refreshMessage := refreshProgress.Message
	progressMutex.RUnlock()

	cacheMutex.RLock()
	defer cacheMutex.RUnlock()

	// Sum sizes from cache
	var totalMovies float64
	for _, m := range cachedMovies.Items {
		totalMovies += m.SizeGB
	}

	var totalTV float64
	for _, s := range cachedSeries {
		totalTV += s.TotalSize
	}

	// Filesystem stats for DATA_PATH
	dataPath := os.Getenv("DATA_PATH")
	if dataPath == "" {
		dataPath = "/data"
	}

	var stat syscall.Statfs_t
	totalFSGB := 0.0
	freeFSGB := 0.0
	usedFSGB := 0.0
	usedPercent := 0.0
	if err := syscall.Statfs(dataPath, &stat); err == nil {
		totalBytes := uint64(stat.Blocks) * uint64(stat.Bsize)
		freeBytes := uint64(stat.Bavail) * uint64(stat.Bsize)
		usedBytes := totalBytes - freeBytes
		totalFSGB = float64(totalBytes) / (1024.0 * 1024.0 * 1024.0)
		freeFSGB = float64(freeBytes) / (1024.0 * 1024.0 * 1024.0)
		usedFSGB = float64(usedBytes) / (1024.0 * 1024.0 * 1024.0)
		if totalBytes > 0 {
			usedPercent = (float64(usedBytes) / float64(totalBytes)) * 100.0
		}
	}

	// Percent of used space occupied by movies vs TV (based on Jellyfin-reported sizes)
	movieOfUsedPct := 0.0
	tvOfUsedPct := 0.0
	if usedFSGB > 0 {
		movieOfUsedPct = (totalMovies / usedFSGB) * 100.0
		tvOfUsedPct = (totalTV / usedFSGB) * 100.0
	}

	// Top lists
	topMovies := make([]models.Movie, len(cachedMovies.Items))
	copy(topMovies, cachedMovies.Items)
	sort.Slice(topMovies, func(i, j int) bool { return topMovies[i].SizeGB > topMovies[j].SizeGB })
	if len(topMovies) > 10 {
		topMovies = topMovies[:10]
	}

	topSeries := make([]models.Series, len(cachedSeries))
	copy(topSeries, cachedSeries)
	sort.Slice(topSeries, func(i, j int) bool { return topSeries[i].TotalSize > topSeries[j].TotalSize })
	if len(topSeries) > 10 {
		topSeries = topSeries[:10]
	}

	data := struct {
		TotalFSGB     float64
		FreeFSGB      float64
		UsedFSGB      float64
		UsedPercent   float64
		TotalMoviesGB float64
		TotalTVGB     float64
		MovieOfUsed   float64
		TVOfUsed      float64
		MoviesCount   int
		SeriesCount   int
		TopMovies     []models.Movie
		TopSeries     []models.Series
		DataPath      string
		IsRefreshing  bool
		RefreshStatus string
		LastRefresh   time.Time
	}{
		TotalFSGB:     totalFSGB,
		FreeFSGB:      freeFSGB,
		UsedFSGB:      usedFSGB,
		UsedPercent:   usedPercent,
		TotalMoviesGB: totalMovies,
		TotalTVGB:     totalTV,
		MovieOfUsed:   movieOfUsedPct,
		TVOfUsed:      tvOfUsedPct,
		MoviesCount:   len(cachedMovies.Items),
		SeriesCount:   len(cachedSeries),
		TopMovies:     topMovies,
		TopSeries:     topSeries,
		DataPath:      dataPath,
		IsRefreshing:  refreshing,
		RefreshStatus: refreshMessage,
		LastRefresh:   lastRefreshTime,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := storageTmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		fmt.Printf("Template error: %v\n", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func handleRefreshMovies(w http.ResponseWriter, r *http.Request) {
	if !startUnifiedScan("movie refresh") {
		w.Write([]byte("Already refreshing"))
		return
	}
	w.Write([]byte("Refresh started"))
}

func handleRefreshTV(w http.ResponseWriter, r *http.Request) {
	if !startUnifiedScan("tv refresh") {
		w.Write([]byte("Already refreshing"))
		return
	}
	w.Write([]byte("Refresh started"))
}

func handleRefreshStatus(w http.ResponseWriter, r *http.Request) {
	progressMutex.RLock()
	progress := refreshProgress
	progressMutex.RUnlock()
	status := scanStatusSnapshot()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"isRefreshing": status.Scanning,
		"message":      progress.Message,
		"current":      progress.Current,
		"total":        progress.Total,
		"scan":         status,
	})
}

func handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	deleteType := r.URL.Query().Get("type")
	idsParam := r.URL.Query().Get("ids")

	if deleteType == "" || idsParam == "" {
		http.Error(w, "Missing type or ids parameter", http.StatusBadRequest)
		return
	}

	ids := []string{}
	for _, id := range splitIDs(idsParam) {
		if id != "" {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		http.Error(w, "No IDs provided", http.StatusBadRequest)
		return
	}

	// Fetch preview items
	previewItems := []struct {
		Name   string
		SizeGB float64
		Path   string
	}{}

	cacheMutex.RLock()
	if deleteType == "movie" {
		for _, id := range ids {
			for _, movie := range cachedMovies.Items {
				if movie.Id == id {
					previewItems = append(previewItems, struct {
						Name   string
						SizeGB float64
						Path   string
					}{
						Name:   movie.Name,
						SizeGB: movie.SizeGB,
						Path:   movie.Path,
					})
					break
				}
			}
		}
	} else if deleteType == "season" {
		for _, id := range ids {
			for _, series := range cachedSeries {
				for _, season := range series.Seasons {
					if season.SeasonId == id {
						previewItems = append(previewItems, struct {
							Name   string
							SizeGB float64
							Path   string
						}{
							Name:   fmt.Sprintf("%s - Season %d", series.Name, season.SeasonNumber),
							SizeGB: season.SizeGB,
							Path:   fmt.Sprintf("Season %d", season.SeasonNumber),
						})
						break
					}
				}
			}
		}
	}
	cacheMutex.RUnlock()

	data := struct {
		Type  string
		Ids   string
		Items []struct {
			Name   string
			SizeGB float64
			Path   string
		}
	}{
		Type:  deleteType,
		Ids:   idsParam,
		Items: previewItems,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := deletePreviewTmpl.Execute(w, data); err != nil {
		fmt.Printf("Template error: %v\n", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func handleDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	var deleteType, idsParam string
	dryRun := false

	// Check for global dry-run mode via environment variable
	// If set, force dry-run mode regardless of request parameters
	envDryRun := os.Getenv("DRY_RUN_MODE")
	if envDryRun == "true" || envDryRun == "1" || strings.ToLower(envDryRun) == "yes" {
		dryRun = true
		fmt.Printf("watched-cleanup: DRY_RUN_MODE environment variable is enabled - all deletions will be in test mode\n")
	}

	// Support both GET (query params) and POST (form/JSON)
	if r.Method == "POST" {
		if r.Header.Get("Content-Type") == "application/json" {
			var req struct {
				Type   string `json:"type"`
				Ids    string `json:"ids"`
				DryRun bool   `json:"dryRun"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				deleteType = req.Type
				idsParam = req.Ids
				// Only use request dryRun if env var is not set
				if envDryRun == "" {
					dryRun = req.DryRun
				}
			}
		} else {
			// Form data
			deleteType = r.FormValue("type")
			idsParam = r.FormValue("ids")
			// Only use request dryRun if env var is not set
			if envDryRun == "" {
				dryRun = r.FormValue("dryRun") == "true" || r.FormValue("test") == "true"
			}
		}
	} else {
		// GET request
		deleteType = r.URL.Query().Get("type")
		idsParam = r.URL.Query().Get("ids")
		// Only use request dryRun if env var is not set
		if envDryRun == "" {
			dryRun = r.URL.Query().Get("dryRun") == "true" || r.URL.Query().Get("test") == "true"
		}
	}

	if deleteType == "" || idsParam == "" {
		http.Error(w, "Missing type or ids", http.StatusBadRequest)
		return
	}

	ids := splitIDs(idsParam)

	if len(ids) == 0 {
		http.Error(w, "No IDs provided", http.StatusBadRequest)
		return
	}

	// Start delete operation in background
	deleteMutex.Lock()
	if isDeleting {
		deleteMutex.Unlock()
		http.Error(w, "Delete already in progress", http.StatusConflict)
		return
	}
	isDeleting = true
	message := "Starting deletion..."
	if dryRun {
		message = "Starting TEST MODE (dry-run) - no files will be deleted..."
	}
	deleteProgress = models.DeleteProgress{
		Total:             len(ids),
		Current:           0,
		Message:           message,
		Percent:           0,
		StageJellyfin:     "pending",
		StageInode:        "pending",
		StageRadarrSonarr: "pending",
		EpisodeCurrent:    0,
		EpisodeTotal:      0,
		StageErrors:       []string{},
	}
	deleteResult = models.DeleteResult{
		Items:       []models.DeletedItem{},
		Errors:      []string{},
		TotalSizeGB: 0,
		DryRun:      dryRun,
	}
	deleteMutex.Unlock()

	// Show progress modal immediately
	w.Header().Set("Content-Type", "text/html")
	deleteProgressTmpl.Execute(w, deleteProgress)

	// Start deletion in background
	go deletion.PerformDelete(httpClient, ids, deleteType, dryRun, &deleteProgress, &deleteResult, &cacheMutex, &cachedMovies, &cachedSeries, &deleteMutex, &isDeleting)
}

func handleDeleteProgress(w http.ResponseWriter, r *http.Request) {
	deleteMutex.RLock()
	progress := deleteProgress
	deleting := isDeleting
	deleteMutex.RUnlock()

	if !deleting {
		// Deletion complete, show summary
		deleteMutex.RLock()
		result := deleteResult
		deleteMutex.RUnlock()

		w.Header().Set("Content-Type", "text/html")
		deleteSummaryTmpl.Execute(w, result)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	deleteProgressTmpl.Execute(w, progress)
}

func splitIDs(idsParam string) []string {
	ids := []string{}
	for _, id := range strings.Split(idsParam, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ids  []string `json:"ids"`
		Type string   `json:"type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var deletedHardlinks []string

	for _, id := range req.Ids {
		var filesToCheck []string

		// Handle different types differently
		if req.Type == "season" {
			// For seasons, we need to get all episodes first
			fmt.Printf("watched-cleanup: Fetching episodes for season %s\n", id)

			seasonEpisodesBody, err := jellyfin.FetchAPI(httpClient, "season_episodes", id)
			if err != nil {
				fmt.Printf("  Error fetching season episodes: %v\n", err)
				continue
			}

			var seasonEpisodes models.EpisodeList
			if err := json.Unmarshal(seasonEpisodesBody, &seasonEpisodes); err != nil {
				fmt.Printf("  Error unmarshaling season episodes: %v\n", err)
				continue
			}

			fmt.Printf("  Found %d episodes in season\n", len(seasonEpisodes.Items))

			// Get the path for each episode
			for _, ep := range seasonEpisodes.Items {
				episodeDetailsBody, err := jellyfin.FetchAPI(httpClient, "episode_details", ep.Id)
				if err != nil {
					continue
				}
				var details models.MovieDetails
				if err := json.Unmarshal(episodeDetailsBody, &details); err != nil {
					continue
				}
				if len(details.MediaSources) > 0 {
					path := details.MediaSources[0].Path
					if path != "" {
						filesToCheck = append(filesToCheck, path)
						fmt.Printf("    Episode file: %s\n", filepath.Base(path))
					}
				}
			}
		} else if req.Type == "movie" {
			// For movies, get the single file path
			detailsBody, err := jellyfin.FetchAPI(httpClient, "movie_details", id)
			if err == nil {
				var details models.MovieDetails
				json.Unmarshal(detailsBody, &details)
				if len(details.MediaSources) > 0 {
					path := details.MediaSources[0].Path
					if path != "" {
						filesToCheck = append(filesToCheck, path)
						fmt.Printf("watched-cleanup: Movie file: %s\n", path)
					}
				}
			}
		}

		// 2. Find and delete hardlinks for all files
		if len(filesToCheck) > 0 {
			fmt.Printf("watched-cleanup: Checking %d file(s) for hardlinks\n", len(filesToCheck))

			for _, filePath := range filesToCheck {
				// Check if file exists
				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					fmt.Printf("  File doesn't exist (already deleted?): %s\n", filePath)
					continue
				}

				hardlinks, err := filesystem.FindHardlinks(filePath, "/data/torrents")
				if err != nil {
					fmt.Printf("  Error finding hardlinks for %s: %v\n", filePath, err)
					continue
				}

				if len(hardlinks) > 0 {
					fmt.Printf("  Found %d hardlink(s) for %s\n", len(hardlinks), filepath.Base(filePath))
					for _, link := range hardlinks {
						fmt.Printf("    Deleting hardlink: %s\n", link)
						if err := os.Remove(link); err != nil {
							fmt.Printf("    Error deleting %s: %v\n", link, err)
						} else {
							deletedHardlinks = append(deletedHardlinks, link)
						}
					}
				} else {
					fmt.Printf("  No hardlinks found for %s\n", filepath.Base(filePath))
				}

				// Delete the original file
				fmt.Printf("  Deleting original file: %s\n", filePath)
				if err := os.Remove(filePath); err != nil {
					fmt.Printf("  Error deleting original: %v\n", err)
				}
			}
		}

		// 3. Tell Jellyfin to delete the item from its database
		fmt.Printf("  Deleting from Jellyfin database: %s\n", id)
		jellyfin.CallJellyfinDelete(httpClient, id)
	}

	responseMsg := fmt.Sprintf("Successfully deleted %d %s(s)", len(req.Ids), req.Type)
	if len(deletedHardlinks) > 0 {
		responseMsg += fmt.Sprintf(" and %d hardlinked torrent file(s)", len(deletedHardlinks))
	}

	fmt.Printf("watched-cleanup: Complete. %s\n", responseMsg)
	w.Write([]byte(responseMsg))
}

// Test handlers for Radarr/Sonarr API
func handleTestRadarrMovies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	baseurl := os.Getenv("RADARR_BASE_URL")
	apiKey := os.Getenv("RADARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		http.Error(w, `{"error": "Radarr not configured"}`, http.StatusInternalServerError)
		return
	}

	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	url := fmt.Sprintf("%sapi/v3/movie", baseurl)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Failed to create request: %v"}`, err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "API request failed: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Failed to read response: %v"}`, err), http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != 200 {
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}

	// Pretty print JSON
	var movies []models.RadarrMovie
	if err := json.Unmarshal(body, &movies); err != nil {
		w.Write(body)
		return
	}

	prettyJSON, _ := json.MarshalIndent(map[string]interface{}{
		"count":  len(movies),
		"movies": movies,
	}, "", "  ")
	w.Write(prettyJSON)
}

func handleTestRadarrSearchPath(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, `{"error": "Missing 'path' query parameter"}`, http.StatusBadRequest)
		return
	}

	movie, err := radarr.SearchByPath(httpClient, filePath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"path":  filePath,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"found": true,
		"movie": movie,
		"path":  filePath,
	})
}

func handleTestRadarrSearchTitle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	title := r.URL.Query().Get("title")
	yearStr := r.URL.Query().Get("year")
	if title == "" {
		http.Error(w, `{"error": "Missing 'title' query parameter"}`, http.StatusBadRequest)
		return
	}

	year := 0
	if yearStr != "" {
		fmt.Sscanf(yearStr, "%d", &year)
	}

	movie, err := radarr.SearchByTitle(httpClient, title, year)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"title": title,
			"year":  year,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"found": true,
		"movie": movie,
		"title": title,
		"year":  year,
	})
}

func handleTestSonarrSeries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	baseurl := os.Getenv("SONARR_BASE_URL")
	apiKey := os.Getenv("SONARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		http.Error(w, `{"error": "Sonarr not configured"}`, http.StatusInternalServerError)
		return
	}

	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	url := fmt.Sprintf("%sapi/v3/series", baseurl)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Failed to create request: %v"}`, err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "API request failed: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Failed to read response: %v"}`, err), http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != 200 {
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}

	// Pretty print JSON
	var seriesList []models.SonarrSeries
	if err := json.Unmarshal(body, &seriesList); err != nil {
		w.Write(body)
		return
	}

	prettyJSON, _ := json.MarshalIndent(map[string]interface{}{
		"count":  len(seriesList),
		"series": seriesList,
	}, "", "  ")
	w.Write(prettyJSON)
}

func handleTestSonarrSearchPath(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, `{"error": "Missing 'path' query parameter"}`, http.StatusBadRequest)
		return
	}

	series, seasonNumber, err := sonarr.SearchByPath(httpClient, filePath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"path":  filePath,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"found":        true,
		"series":       series,
		"seasonNumber": seasonNumber,
		"path":         filePath,
	})
}

func handleTestSonarrSearchTitle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	title := r.URL.Query().Get("title")
	if title == "" {
		http.Error(w, `{"error": "Missing 'title' query parameter"}`, http.StatusBadRequest)
		return
	}

	// Season number is optional for title search
	seasonNumber := 0
	if seasonStr := r.URL.Query().Get("season"); seasonStr != "" {
		fmt.Sscanf(seasonStr, "%d", &seasonNumber)
	}

	series, err := sonarr.SearchByTitle(httpClient, title, seasonNumber)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":        err.Error(),
			"title":        title,
			"seasonNumber": seasonNumber,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"found":        true,
		"series":       series,
		"title":        title,
		"seasonNumber": seasonNumber,
	})
}
