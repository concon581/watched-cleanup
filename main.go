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
	"sync/atomic"
	"syscall"
	"time"
)

type Movie struct {
	Id             string            `json:"Id"`
	Name           string            `json:"Name"`
	Overview       string            `json:"Overview"`
	ProductionYear int               `json:"ProductionYear"`
	ImageTags      map[string]string `json:"ImageTags"`
	Size           int64             `json:"Size"`
	SizeGB         float64           `json:"-"`
	Path           string            `json:"-"`
}

type MediaSource struct {
	Size int64  `json:"Size"`
	Path string `json:"Path"`
}

type MovieDetails struct {
	MediaSources []MediaSource `json:"MediaSources"`
}

type MovieList struct {
	Items []Movie `json:"Items"`
}

type Episode struct {
	Id                string `json:"Id"`
	Name              string `json:"Name"`
	SeriesId          string `json:"SeriesId"`
	SeriesName        string `json:"SeriesName"`
	SeasonId          string `json:"SeasonId"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
	IndexNumber       int    `json:"IndexNumber"`
}

type SeasonDetails struct {
	Id         string `json:"Id"`
	Name       string `json:"Name"`
	ChildCount int    `json:"ChildCount"`
}

type EpisodeList struct {
	Items []Episode `json:"Items"`
}

type SeasonList struct {
	Items []SeasonDetails `json:"Items"`
}

type SeasonInfo struct {
	SeasonNumber int
	SeasonId     string
	WatchedCount int
	TotalCount   int
	SizeGB       float64
}

type Series struct {
	Name      string
	Id        string
	ImageTags map[string]string `json:"ImageTags"`
	Seasons   []SeasonInfo
	TotalSize float64
}

type RefreshProgress struct {
	Current int
	Total   int
	Message string
}

type DeleteProgress struct {
	Current     int
	Total       int
	Message     string
	CurrentItem string
	Percent     int
}

type DeleteResult struct {
	DeletedCount     int
	DeletedHardlinks int
	Errors           []string
	Details          []struct {
		Name string
		Path string
	}
}

var (
	cachedMovies    MovieList
	cachedSeries    []Series
	isRefreshing    bool
	lastRefresh     time.Time
	cacheMutex      sync.RWMutex
	refreshProgress RefreshProgress
	progressMutex   sync.RWMutex

	// Delete progress tracking
	deleteProgress DeleteProgress
	deleteResult   DeleteResult
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
		return // Can't open file, skip silently
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
	fmt.Println("watched-cleanup v1.0.1 - hardlink test starting...")
	fmt.Println("Server starting on :6969")
	http.ListenAndServe(":6969", nil)

}

func fetchAPI(request_type string, id string) ([]byte, error) {
	jellyfin_user_id := os.Getenv("JELLYFIN_USER_ID")

	var api string
	if request_type == "played_movies" {
		api = "Users/" + jellyfin_user_id + "/Items?Recursive=true&IsPlayed=true&IncludeItemTypes=Movie"
	} else if request_type == "watched_episodes" {
		api = "Users/" + jellyfin_user_id + "/Items?Recursive=true&IsPlayed=true&IncludeItemTypes=Episode"
	} else if request_type == "season_details" {
		api = "Items/" + id + "?userId=" + jellyfin_user_id
	} else if request_type == "season_info" {
		api = "Items/" + id + "?userId=" + jellyfin_user_id
	} else if request_type == "series_seasons" {
		api = "Shows/" + id + "/Seasons?userId=" + jellyfin_user_id
	} else if request_type == "movie_details" {
		api = "Users/" + jellyfin_user_id + "/Items/" + id
	} else if request_type == "episode_details" {
		api = "Users/" + jellyfin_user_id + "/Items/" + id
	} else if request_type == "season_episodes" {
		api = "Users/" + jellyfin_user_id + "/Items?ParentId=" + id + "&Fields=MediaSources"
	} else if request_type == "series_details" {
		api = "Users/" + jellyfin_user_id + "/Items/" + id
	}

	baseurl := os.Getenv("JELLYFIN_BASE_URL")

	url := baseurl + api

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	token := os.Getenv("JELLYFIN_API_KEY")

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func updateProgress(current, total int, message string) {
	progressMutex.Lock()
	refreshProgress = RefreshProgress{
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
		Items       []Movie
		LastRefresh time.Time
	}{
		Items:       cachedMovies.Items,
		LastRefresh: lastRefresh,
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
		Series
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
		Series      []EnhancedSeries
		LastRefresh time.Time
	}{
		Series:      enhancedSeries,
		LastRefresh: lastRefresh,
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
		newMovies := fetchMovieData()

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
		newSeries := fetchTVData()

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

	// Support both GET (query params) and POST (form/JSON)
	if r.Method == "POST" {
		if r.Header.Get("Content-Type") == "application/json" {
			var req struct {
				Type string `json:"type"`
				Ids  string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				deleteType = req.Type
				idsParam = req.Ids
			}
		} else {
			// Form data
			deleteType = r.FormValue("type")
			idsParam = r.FormValue("ids")
		}
	} else {
		// GET request
		deleteType = r.URL.Query().Get("type")
		idsParam = r.URL.Query().Get("ids")
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
	deleteProgress = DeleteProgress{
		Total:   len(ids),
		Current: 0,
		Message: "Starting deletion...",
		Percent: 0,
	}
	deleteResult = DeleteResult{
		Details: []struct {
			Name string
			Path string
		}{},
		Errors: []string{},
	}
	deleteMutex.Unlock()

	// Show progress modal immediately
	w.Header().Set("Content-Type", "text/html")
	deleteProgressTmpl.Execute(w, deleteProgress)

	// Start deletion in background
	go performDelete(ids, deleteType)
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

func updateDeleteProgress(current, total int, message, currentItem string) {
	deleteMutex.Lock()
	deleteProgress = DeleteProgress{
		Current:     current,
		Total:       total,
		Message:     message,
		CurrentItem: currentItem,
		Percent:     int(float64(current) / float64(total) * 100),
	}
	deleteMutex.Unlock()
}

func performDelete(ids []string, deleteType string) {
	fmt.Printf("watched-cleanup: Starting deletion of %d %s(s)\n", len(ids), deleteType)

	defer func() {
		deleteMutex.Lock()
		isDeleting = false
		deleteMutex.Unlock()
		fmt.Printf("watched-cleanup: Deletion process completed\n")
	}()

	var deletedHardlinks []string
	var errors []string
	var details []struct {
		Name string
		Path string
	}

	// Get hardlink search directory from env, default to /data/torrents for Docker
	hardlinkSearchDir := os.Getenv("HARDLINK_SEARCH_DIR")
	if hardlinkSearchDir == "" {
		hardlinkSearchDir = "/data/torrents"
	}
	fmt.Printf("watched-cleanup: Using hardlink search directory: %s\n", hardlinkSearchDir)

	for i, id := range ids {
		var filesToCheck []string
		var itemName string

		// Handle different types differently
		if deleteType == "season" {
			fmt.Printf("watched-cleanup: Processing season %d/%d (ID: %s)\n", i+1, len(ids), id)
			updateDeleteProgress(i+1, len(ids), fmt.Sprintf("Processing season %d of %d", i+1, len(ids)), "")

			seasonEpisodesBody, err := fetchAPI("season_episodes", id)
			if err != nil {
				errMsg := fmt.Sprintf("Error fetching season episodes: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				errors = append(errors, errMsg)
				continue
			}

			var seasonEpisodes EpisodeList
			if err := json.Unmarshal(seasonEpisodesBody, &seasonEpisodes); err != nil {
				errMsg := fmt.Sprintf("Error parsing season episodes: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				errors = append(errors, errMsg)
				continue
			}

			fmt.Printf("watched-cleanup: Found %d episodes in season\n", len(seasonEpisodes.Items))

			// Get season name
			cacheMutex.RLock()
			for _, series := range cachedSeries {
				for _, season := range series.Seasons {
					if season.SeasonId == id {
						itemName = fmt.Sprintf("%s - Season %d", series.Name, season.SeasonNumber)
						break
					}
				}
			}
			cacheMutex.RUnlock()
			fmt.Printf("watched-cleanup: Season name: %s\n", itemName)

			for _, ep := range seasonEpisodes.Items {
				episodeDetailsBody, err := fetchAPI("episode_details", ep.Id)
				if err != nil {
					fmt.Printf("watched-cleanup: Error fetching episode %s: %v\n", ep.Id, err)
					continue
				}
				var details MovieDetails
				if err := json.Unmarshal(episodeDetailsBody, &details); err != nil {
					fmt.Printf("watched-cleanup: Error parsing episode %s: %v\n", ep.Id, err)
					continue
				}
				if len(details.MediaSources) > 0 {
					path := details.MediaSources[0].Path
					if path != "" {
						filesToCheck = append(filesToCheck, path)
						fmt.Printf("watched-cleanup: Episode file: %s\n", filepath.Base(path))
					}
				}
			}
		} else if deleteType == "movie" {
			fmt.Printf("watched-cleanup: Processing movie %d/%d (ID: %s)\n", i+1, len(ids), id)
			updateDeleteProgress(i+1, len(ids), fmt.Sprintf("Processing movie %d of %d", i+1, len(ids)), "")

			detailsBody, err := fetchAPI("movie_details", id)
			if err != nil {
				errMsg := fmt.Sprintf("Error fetching movie details: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				errors = append(errors, errMsg)
				continue
			}

			var details MovieDetails
			if err := json.Unmarshal(detailsBody, &details); err != nil {
				errMsg := fmt.Sprintf("Error parsing movie details: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				errors = append(errors, errMsg)
				continue
			}

			if len(details.MediaSources) > 0 {
				path := details.MediaSources[0].Path
				if path != "" {
					filesToCheck = append(filesToCheck, path)
					fmt.Printf("watched-cleanup: Movie file: %s\n", path)
				}

				// Get movie name
				cacheMutex.RLock()
				for _, movie := range cachedMovies.Items {
					if movie.Id == id {
						itemName = movie.Name
						break
					}
				}
				cacheMutex.RUnlock()
				fmt.Printf("watched-cleanup: Movie name: %s\n", itemName)
			}
		}

		// Find and delete hardlinks
		if len(filesToCheck) > 0 {
			fmt.Printf("watched-cleanup: Checking %d file(s) for hardlinks\n", len(filesToCheck))
			for _, filePath := range filesToCheck {
				updateDeleteProgress(i+1, len(ids), fmt.Sprintf("Deleting files for %s", itemName), filepath.Base(filePath))
				fmt.Printf("watched-cleanup: Processing file: %s\n", filePath)

				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					fmt.Printf("watched-cleanup: File doesn't exist (already deleted?): %s\n", filePath)
					continue
				}

				// Check if hardlink search directory exists
				if _, err := os.Stat(hardlinkSearchDir); os.IsNotExist(err) {
					fmt.Printf("watched-cleanup: Hardlink search directory doesn't exist, skipping hardlink check: %s\n", hardlinkSearchDir)
				} else {
					hardlinks, err := findHardlinks(filePath, hardlinkSearchDir)
					if err != nil {
						errMsg := fmt.Sprintf("Error finding hardlinks for %s: %v", filePath, err)
						fmt.Printf("watched-cleanup: %s\n", errMsg)
						errors = append(errors, errMsg)
					} else {
						if len(hardlinks) > 0 {
							fmt.Printf("watched-cleanup: Found %d hardlink(s) for %s\n", len(hardlinks), filepath.Base(filePath))
							for _, link := range hardlinks {
								fmt.Printf("watched-cleanup: Deleting hardlink: %s\n", link)
								if err := os.Remove(link); err != nil {
									errMsg := fmt.Sprintf("Error deleting hardlink %s: %v", link, err)
									fmt.Printf("watched-cleanup: %s\n", errMsg)
									errors = append(errors, errMsg)
								} else {
									deletedHardlinks = append(deletedHardlinks, link)
									fmt.Printf("watched-cleanup: Successfully deleted hardlink: %s\n", link)
								}
							}
						} else {
							fmt.Printf("watched-cleanup: No hardlinks found for %s\n", filepath.Base(filePath))
						}
					}
				}

				// Delete the original file
				fmt.Printf("watched-cleanup: Deleting original file: %s\n", filePath)
				if err := os.Remove(filePath); err != nil {
					errMsg := fmt.Sprintf("Error deleting %s: %v", filePath, err)
					fmt.Printf("watched-cleanup: %s\n", errMsg)
					errors = append(errors, errMsg)
				} else {
					fmt.Printf("watched-cleanup: Successfully deleted file: %s\n", filePath)
				}
			}
		} else {
			fmt.Printf("watched-cleanup: No files to delete for %s\n", itemName)
		}

		// Delete from Jellyfin
		fmt.Printf("watched-cleanup: Deleting from Jellyfin database: %s (%s)\n", id, itemName)
		updateDeleteProgress(i+1, len(ids), fmt.Sprintf("Removing from Jellyfin: %s", itemName), "")
		callJellyfinDelete(id)
		fmt.Printf("watched-cleanup: Jellyfin delete request sent for: %s\n", id)

		details = append(details, struct {
			Name string
			Path string
		}{
			Name: itemName,
			Path: "",
		})
	}

	// Update final result
	fmt.Printf("watched-cleanup: Deletion summary:\n")
	fmt.Printf("watched-cleanup:   - Items deleted: %d\n", len(ids))
	fmt.Printf("watched-cleanup:   - Hardlinks deleted: %d\n", len(deletedHardlinks))
	fmt.Printf("watched-cleanup:   - Errors: %d\n", len(errors))
	if len(errors) > 0 {
		for _, err := range errors {
			fmt.Printf("watched-cleanup:     * %s\n", err)
		}
	}

	deleteMutex.Lock()
	deleteResult = DeleteResult{
		DeletedCount:     len(ids),
		DeletedHardlinks: len(deletedHardlinks),
		Errors:           errors,
		Details:          details,
	}
	deleteMutex.Unlock()
}
func callJellyfinDelete(id string) {
	baseurl := os.Getenv("JELLYFIN_BASE_URL")
	token := os.Getenv("JELLYFIN_API_KEY")
	url := fmt.Sprintf("%sItems/%s", baseurl, id)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		fmt.Printf("watched-cleanup: Error creating Jellyfin delete request: %v\n", err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("watched-cleanup: Error sending Jellyfin delete request: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Printf("watched-cleanup: Jellyfin delete successful (HTTP %d)\n", resp.StatusCode)
	} else {
		fmt.Printf("watched-cleanup: Jellyfin delete returned HTTP %d\n", resp.StatusCode)
	}
}

// Add this function to find hardlinks by inode
func findHardlinks(targetPath string, searchDir string) ([]string, error) {
	// Get the inode of the target file
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}

	targetStat, ok := targetInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("failed to get stat info")
	}
	targetInode := targetStat.Ino

	var matches []string

	// Walk the search directory
	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if info.IsDir() {
			return nil // Skip directories
		}

		// Get inode of current file
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}

		// If inodes match, this is a hardlink
		if stat.Ino == targetInode {
			matches = append(matches, path)
		}

		return nil
	})

	return matches, err
}

// Add this function to find all files in a directory recursively
func getAllFilesInDir(dirPath string) ([]string, error) {
	var files []string

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if !info.IsDir() {
			files = append(files, path)
		}

		return nil
	})

	return files, err
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

			seasonEpisodesBody, err := fetchAPI("season_episodes", id)
			if err != nil {
				fmt.Printf("  Error fetching season episodes: %v\n", err)
				continue
			}

			var seasonEpisodes EpisodeList
			if err := json.Unmarshal(seasonEpisodesBody, &seasonEpisodes); err != nil {
				fmt.Printf("  Error unmarshaling season episodes: %v\n", err)
				continue
			}

			fmt.Printf("  Found %d episodes in season\n", len(seasonEpisodes.Items))

			// Get the path for each episode
			for _, ep := range seasonEpisodes.Items {
				episodeDetailsBody, err := fetchAPI("episode_details", ep.Id)
				if err != nil {
					continue
				}
				var details MovieDetails
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
			detailsBody, err := fetchAPI("movie_details", id)
			if err == nil {
				var details MovieDetails
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

				hardlinks, err := findHardlinks(filePath, "/data/torrents")
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
		callJellyfinDelete(id)
	}

	responseMsg := fmt.Sprintf("Successfully deleted %d %s(s)", len(req.Ids), req.Type)
	if len(deletedHardlinks) > 0 {
		responseMsg += fmt.Sprintf(" and %d hardlinked torrent file(s)", len(deletedHardlinks))
	}

	fmt.Printf("watched-cleanup: Complete. %s\n", responseMsg)
	w.Write([]byte(responseMsg))
}

func fetchMovieData() MovieList {
	body, err := fetchAPI("played_movies", "")
	if err != nil {
		fmt.Println("Error fetching movies:", err)
		return MovieList{}
	}

	var movieList MovieList
	if err := json.Unmarshal(body, &movieList); err != nil {
		fmt.Println("Error parsing movies:", err)
		return MovieList{}
	}

	fmt.Println("Number of movies:", len(movieList.Items))

	// Parallelize movie detail fetching
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Limit to 10 concurrent requests
	var mu sync.Mutex
	var completed int32

	for i := range movieList.Items {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			detailsBody, err := fetchAPI("movie_details", movieList.Items[idx].Id)
			if err != nil {
				atomic.AddInt32(&completed, 1)
				return
			}
			var details MovieDetails
			if err := json.Unmarshal(detailsBody, &details); err != nil {
				atomic.AddInt32(&completed, 1)
				return
			}
			if len(details.MediaSources) > 0 {
				mu.Lock()
				movieList.Items[idx].Size = details.MediaSources[0].Size
				movieList.Items[idx].SizeGB = float64(details.MediaSources[0].Size) / (1024 * 1024 * 1024)
				movieList.Items[idx].Path = details.MediaSources[0].Path
				mu.Unlock()
			}

			current := atomic.AddInt32(&completed, 1)
			updateProgress(int(current), len(movieList.Items), fmt.Sprintf("Fetching movie %d/%d", int(current), len(movieList.Items)))
		}(i)
	}

	wg.Wait()
	return movieList
}

func fetchTVData() []Series {
	body, err := fetchAPI("watched_episodes", "")
	if err != nil {
		fmt.Println("Error fetching episodes:", err)
		return nil
	}

	var episodeList EpisodeList
	if err := json.Unmarshal(body, &episodeList); err != nil {
		fmt.Println("Error parsing episodes:", err)
		return nil
	}

	fmt.Println("Unmarshaled", len(episodeList.Items), "episodes")

	grouped := make(map[string]*Series)

	for _, ep := range episodeList.Items {
		seriesId := ep.SeriesId
		if grouped[seriesId] == nil {
			// Fetch series details to get artwork
			seriesBody, err := fetchAPI("series_details", seriesId)
			if err == nil {
				var seriesDetails struct {
					ImageTags map[string]string `json:"ImageTags"`
				}
				json.Unmarshal(seriesBody, &seriesDetails)
				grouped[seriesId] = &Series{
					Name:      ep.SeriesName,
					Id:        seriesId,
					ImageTags: seriesDetails.ImageTags,
				}
			} else {
				grouped[seriesId] = &Series{
					Name:      ep.SeriesName,
					Id:        seriesId,
					ImageTags: make(map[string]string),
				}
			}
			fmt.Println("New series:", ep.SeriesName)
		}
		seasonNum := ep.ParentIndexNumber
		found := false
		for i := range grouped[seriesId].Seasons {
			if grouped[seriesId].Seasons[i].SeasonNumber == seasonNum {
				grouped[seriesId].Seasons[i].WatchedCount++
				found = true
				break
			}
		}
		if !found {
			grouped[seriesId].Seasons = append(grouped[seriesId].Seasons, SeasonInfo{
				SeasonNumber: seasonNum,
				SeasonId:     ep.SeasonId,
				WatchedCount: 1,
			})
			fmt.Printf("  New season %d for %s (SeasonId: %s)\n", seasonNum, ep.SeriesName, ep.SeasonId)
		}
	}

	fmt.Println("Fetching season details for", len(grouped), "series...")

	totalSeries := len(grouped)
	var seriesCompleted int32

	// Process each series
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, series := range grouped {
		wg.Add(1)
		go func(s *Series) {
			defer wg.Done()

			fmt.Println("Processing series:", s.Name, "with", len(s.Seasons), "seasons")

			// Process seasons for this series
			var seasonWg sync.WaitGroup
			seasonSem := make(chan struct{}, 5) // Limit concurrent season fetches

			for i := range s.Seasons {
				seasonWg.Add(1)
				go func(idx int) {
					defer seasonWg.Done()
					seasonSem <- struct{}{}
					defer func() { <-seasonSem }()

					fmt.Printf("  Fetching info for season %d (ID: %s)\n", s.Seasons[idx].SeasonNumber, s.Seasons[idx].SeasonId)

					seasonInfoBody, err := fetchAPI("season_info", s.Seasons[idx].SeasonId)
					if err != nil {
						fmt.Println("    Error fetching season info:", err)
						return
					}
					var seasonInfo SeasonDetails
					if err := json.Unmarshal(seasonInfoBody, &seasonInfo); err != nil {
						fmt.Println("    Error unmarshaling season info:", err)
						return
					}
					s.Seasons[idx].TotalCount = seasonInfo.ChildCount
					fmt.Printf("    Season %d: %d episodes total\n", s.Seasons[idx].SeasonNumber, seasonInfo.ChildCount)

					seasonEpisodesBody, err := fetchAPI("season_episodes", s.Seasons[idx].SeasonId)
					if err != nil {
						fmt.Println("    Error fetching season episodes:", err)
						return
					}
					var seasonEpisodes EpisodeList
					if err := json.Unmarshal(seasonEpisodesBody, &seasonEpisodes); err != nil {
						fmt.Println("    Error unmarshaling season episodes:", err)
						return
					}

					fmt.Printf("    Fetching sizes for %d episodes\n", len(seasonEpisodes.Items))

					// Parallelize episode size fetching
					var epWg sync.WaitGroup
					epSem := make(chan struct{}, 10) // Limit concurrent episode fetches
					var sizeMu sync.Mutex
					var totalSize int64

					for _, ep := range seasonEpisodes.Items {
						epWg.Add(1)
						go func(episode Episode) {
							defer epWg.Done()
							epSem <- struct{}{}
							defer func() { <-epSem }()

							episodeDetailsBody, err := fetchAPI("episode_details", episode.Id)
							if err != nil {
								return
							}
							var details MovieDetails
							if err := json.Unmarshal(episodeDetailsBody, &details); err != nil {
								return
							}
							if len(details.MediaSources) > 0 {
								sizeMu.Lock()
								totalSize += details.MediaSources[0].Size
								sizeMu.Unlock()
							}
						}(ep)
					}

					epWg.Wait()
					s.Seasons[idx].SizeGB = float64(totalSize) / (1024 * 1024 * 1024)

					mu.Lock()
					s.TotalSize += s.Seasons[idx].SizeGB
					mu.Unlock()

					fmt.Printf("    Season %d: %.2f GB total\n", s.Seasons[idx].SeasonNumber, s.Seasons[idx].SizeGB)
				}(i)
			}

			seasonWg.Wait()

			// Update progress after series completes
			current := atomic.AddInt32(&seriesCompleted, 1)
			updateProgress(int(current), totalSeries, fmt.Sprintf("Completed %d/%d series", int(current), totalSeries))
		}(series)
	}

	wg.Wait()

	var seriesList []Series
	for _, s := range grouped {
		seriesList = append(seriesList, *s)
	}

	// Sort by name by default
	sort.Slice(seriesList, func(i, j int) bool {
		return seriesList[i].Name < seriesList[j].Name
	})

	fmt.Println("Returning", len(seriesList), "series")
	return seriesList
}
