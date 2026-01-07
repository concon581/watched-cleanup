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
	"strings"
	"sync"
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
)

// Load templates from files
var tmpl *template.Template
var tvTmpl *template.Template
var deletePreviewTmpl *template.Template
var deleteProgressTmpl *template.Template
var deleteSummaryTmpl *template.Template

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
	http.HandleFunc("/refresh", handleRefreshMovies)
	http.HandleFunc("/refresh-tv", handleRefreshTV)
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

func handleRefreshMovies(w http.ResponseWriter, r *http.Request) {
	cacheMutex.Lock()
	if isRefreshing {
		cacheMutex.Unlock()
		w.Write([]byte("Already refreshing"))
		return
	}
	isRefreshing = true
	cacheMutex.Unlock()

	go func() {
		fmt.Println("Starting movie refresh...")
		updateProgress(0, 0, "Fetching movie list...")
		newMovies := jellyfin.FetchMovieData(httpClient, updateProgress)

		cacheMutex.Lock()
		cachedMovies = newMovies
		lastRefresh = time.Now()
		isRefreshing = false
		cacheMutex.Unlock()

		fmt.Println("Movie refresh complete!")
	}()

	w.Write([]byte("Refresh started"))
}

func handleRefreshTV(w http.ResponseWriter, r *http.Request) {
	cacheMutex.Lock()
	if isRefreshing {
		cacheMutex.Unlock()
		w.Write([]byte("Already refreshing"))
		return
	}
	isRefreshing = true
	cacheMutex.Unlock()

	go func() {
		fmt.Println("Starting TV refresh...")
		updateProgress(0, 0, "Fetching episode list...")
		newSeries := jellyfin.FetchTVData(httpClient, updateProgress)

		cacheMutex.Lock()
		cachedSeries = newSeries
		lastRefresh = time.Now()
		isRefreshing = false
		cacheMutex.Unlock()

		fmt.Println("TV refresh complete!")
	}()

	w.Write([]byte("Refresh started"))
}

func handleRefreshStatus(w http.ResponseWriter, r *http.Request) {
	cacheMutex.RLock()
	refreshing := isRefreshing
	cacheMutex.RUnlock()

	progressMutex.RLock()
	progress := refreshProgress
	progressMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"isRefreshing": refreshing,
		"message":      progress.Message,
		"current":      progress.Current,
		"total":        progress.Total,
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
		Items:      []models.DeletedItem{},
		Errors:     []string{},
		TotalSizeGB: 0,
		DryRun:     dryRun,
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

