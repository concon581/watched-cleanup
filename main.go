package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
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

var (
	cachedMovies     MovieList
	cachedSeries     []Series
	isRefreshing     bool
	lastRefresh      time.Time
	cacheMutex       sync.RWMutex
	refreshProgress  RefreshProgress
	progressMutex    sync.RWMutex
)

var htmlTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Watched Movies</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; background: #1a1a1a; color: #fff; }
        .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
        .refresh-btn { 
            padding: 10px 20px; 
            background: #4CAF50; 
            color: white; 
            border: none; 
            border-radius: 4px; 
            cursor: pointer; 
            font-size: 16px;
        }
        .refresh-btn:disabled { background: #666; cursor: not-allowed; }
        .status { color: #aaa; font-size: 14px; }
        .movie { 
            margin-bottom: 20px; 
            border: 1px solid #333; 
            padding: 15px; 
            background: #2a2a2a;
            border-radius: 4px;
            display: flex;
            gap: 15px;
        }
        .movie-content { flex: 1; }
        .movie-checkbox { 
            display: flex; 
            align-items: center; 
        }
        .movie-checkbox input { 
            width: 20px; 
            height: 20px; 
            cursor: pointer; 
        }
        img { max-width: 200px; height: auto; border-radius: 4px; }
        .delete-btn {
            padding: 12px 24px;
            background: #f44336;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 16px;
            margin-top: 20px;
        }
        .delete-btn:disabled { background: #666; cursor: not-allowed; }
        h1 { margin: 0; }
    </style>
</head>
<body>
    <div class="header">
        <div>
            <h1>Watched Movies</h1>
            <div class="status" id="status">
                {{if .LastRefresh.IsZero}}
                    No data loaded. Click refresh to load.
                {{else}}
                    Last updated: {{.LastRefresh.Format "Jan 2, 2006 3:04 PM"}}
                {{end}}
            </div>
        </div>
        <button class="refresh-btn" onclick="refreshData()" id="refreshBtn">Refresh Data</button>
    </div>
    
    {{if .Items}}
    <button class="delete-btn" onclick="deleteSelected()">Delete Selected</button>
    
    {{range .Items}}
    <div class="movie">
        <div class="movie-checkbox">
            <input type="checkbox" name="movie" value="{{.Id}}" data-path="{{.Path}}">
        </div>
        <div class="movie-content">
            <h2>{{.Name}} {{if .ProductionYear}}({{.ProductionYear}}){{end}}</h2>
            <p>{{.Overview}}</p>
            <p><strong>Size:</strong> {{printf "%.2f" .SizeGB}} GB</p>
            <p><strong>Path:</strong> {{.Path}}</p>
        </div>
        {{if .ImageTags.Primary}}
        <img src="http://nas.home.arpa:8096/Items/{{.Id}}/Images/Primary?maxWidth=200" alt="{{.Name}}">
        {{end}}
    </div>
    {{end}}
    {{else}}
    <p style="color: #aaa;">No movies loaded. Click "Refresh Data" to fetch from Jellyfin.</p>
    {{end}}
    
    <script>
        function refreshData() {
            const btn = document.getElementById('refreshBtn');
            const status = document.getElementById('status');
            btn.disabled = true;
            btn.textContent = 'Refreshing...';
            status.textContent = 'Fetching data from Jellyfin...';
            
            fetch('/refresh', { method: 'POST' })
                .then(r => r.text())
                .then(() => {
                    checkRefreshStatus();
                });
        }
        
        function checkRefreshStatus() {
            fetch('/refresh-status')
                .then(r => r.json())
                .then(data => {
                    if (data.isRefreshing) {
                        document.getElementById('status').textContent = data.message;
                        setTimeout(checkRefreshStatus, 1000);
                    } else {
                        location.reload();
                    }
                });
        }
        
        function deleteSelected() {
            const checked = document.querySelectorAll('input[name="movie"]:checked');
            if (checked.length === 0) {
                alert('Please select at least one movie to delete');
                return;
            }
            
            if (!confirm('Delete ' + checked.length + ' movie(s)? This cannot be undone!')) {
                return;
            }
            
            const ids = Array.from(checked).map(cb => cb.value);
            
            fetch('/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ids: ids, type: 'movie' })
            })
            .then(r => r.text())
            .then(msg => {
                alert(msg);
                location.reload();
            });
        }
    </script>
</body>
</html>`

var tmpl = template.Must(template.New("movies").Funcs(template.FuncMap{
	"formatTime": func(t time.Time) string {
		return t.Format("Jan 2, 2006 3:04 PM")
	},
}).Parse(htmlTemplate))

var tvTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Watched TV Shows</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; background: #1a1a1a; color: #fff; }
        .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
        .controls { display: flex; gap: 10px; margin-bottom: 20px; align-items: center; }
        .refresh-btn { 
            padding: 10px 20px; 
            background: #4CAF50; 
            color: white; 
            border: none; 
            border-radius: 4px; 
            cursor: pointer; 
            font-size: 16px;
        }
        .refresh-btn:disabled { background: #666; cursor: not-allowed; }
        .status { color: #aaa; font-size: 14px; }
        .series { 
            margin-bottom: 20px; 
            border: 1px solid #333; 
            padding: 15px; 
            background: #2a2a2a;
            border-radius: 4px;
            display: flex;
            gap: 15px;
        }
        .series-content { flex: 1; }
        .season { 
            margin-left: 20px; 
            padding: 10px;
            border-left: 3px solid #4CAF50;
            margin-top: 10px;
            display: flex;
            align-items: center;
            gap: 15px;
        }
        .season input[type="checkbox"] {
            width: 20px;
            height: 20px;
            cursor: pointer;
        }
        img { max-width: 200px; height: auto; border-radius: 4px; }
        .delete-btn {
            padding: 12px 24px;
            background: #f44336;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 16px;
        }
        .delete-btn:disabled { background: #666; cursor: not-allowed; }
        h1 { margin: 0; }
        select, input[type="number"] {
            padding: 8px;
            background: #333;
            color: #fff;
            border: 1px solid #555;
            border-radius: 4px;
            font-size: 14px;
        }
        label { color: #aaa; font-size: 14px; }
        .select-all { margin-left: 10px; }
    </style>
</head>
<body>
    <div class="header">
        <div>
            <h1>Watched TV Shows</h1>
            <div class="status" id="status">
                {{if .LastRefresh.IsZero}}
                    No data loaded. Click refresh to load.
                {{else}}
                    Last updated: {{.LastRefresh.Format "Jan 2, 2006 3:04 PM"}}
                {{end}}
            </div>
        </div>
        <button class="refresh-btn" onclick="refreshData()" id="refreshBtn">Refresh Data</button>
    </div>
    
    {{if .Series}}
    <div class="controls">
        <label>Sort by:</label>
        <select id="sortBy" onchange="applyFilters()">
            <option value="name">Name</option>
            <option value="size-desc">Size (Largest First)</option>
            <option value="size-asc">Size (Smallest First)</option>
        </select>
        
        <label>Min Size (GB):</label>
        <input type="number" id="minSize" value="0" min="0" step="5" onchange="applyFilters()" style="width: 80px;">
        
        <label>
            <input type="checkbox" id="fullyWatched" onchange="applyFilters()">
            Fully watched only
        </label>
        
        <button class="select-all" onclick="selectAll()">Select All</button>
        <button class="select-all" onclick="deselectAll()">Deselect All</button>
    </div>
    
    <button class="delete-btn" onclick="deleteSelected()">Delete Selected Seasons</button>
    
    <div id="seriesList">
    {{range .Series}}
    <div class="series" data-size="{{.TotalSize}}" data-name="{{.Name}}" data-fully-watched="{{.FullyWatched}}">
        {{if .ImageTags.Primary}}
        <img src="http://nas.home.arpa:8096/Items/{{.Id}}/Images/Primary?maxWidth=200" alt="{{.Name}}">
        {{end}}
        <div class="series-content">
            <h2>{{.Name}} ({{printf "%.2f" .TotalSize}} GB total)</h2>
            {{range .Seasons}}
            <div class="season">
                <input type="checkbox" name="season" value="{{.SeasonId}}">
                <div>
                    <strong>Season {{.SeasonNumber}}:</strong> {{.WatchedCount}}/{{.TotalCount}} episodes watched ({{printf "%.2f" .SizeGB}} GB)
                </div>
            </div>
            {{end}}
        </div>
    </div>
    {{end}}
    </div>
    {{else}}
    <p style="color: #aaa;">No TV shows loaded. Click "Refresh Data" to fetch from Jellyfin.</p>
    {{end}}
    
    <script>
        function refreshData() {
            const btn = document.getElementById('refreshBtn');
            const status = document.getElementById('status');
            btn.disabled = true;
            btn.textContent = 'Refreshing...';
            status.textContent = 'Starting refresh...';
            
            fetch('/refresh-tv', { method: 'POST' })
                .then(r => r.text())
                .then(() => {
                    checkRefreshStatus();
                });
        }
        
        function checkRefreshStatus() {
            fetch('/refresh-status')
                .then(r => r.json())
                .then(data => {
                    if (data.isRefreshing) {
                        document.getElementById('status').textContent = data.message;
                        setTimeout(checkRefreshStatus, 1000);
                    } else {
                        location.reload();
                    }
                });
        }
        
        function applyFilters() {
            const sortBy = document.getElementById('sortBy').value;
            const minSize = parseFloat(document.getElementById('minSize').value) || 0;
            const fullyWatched = document.getElementById('fullyWatched').checked;
            
            const container = document.getElementById('seriesList');
            const series = Array.from(container.children);
            
            series.forEach(s => {
                const size = parseFloat(s.dataset.size);
                const isFullyWatched = s.dataset.fullyWatched === 'true';
                
                let show = size >= minSize;
                if (fullyWatched && !isFullyWatched) show = false;
                
                s.style.display = show ? 'flex' : 'none';
            });
            
            const visibleSeries = series.filter(s => s.style.display !== 'none');
            
            visibleSeries.sort((a, b) => {
                if (sortBy === 'name') {
                    return a.dataset.name.localeCompare(b.dataset.name);
                } else if (sortBy === 'size-desc') {
                    return parseFloat(b.dataset.size) - parseFloat(a.dataset.size);
                } else if (sortBy === 'size-asc') {
                    return parseFloat(a.dataset.size) - parseFloat(b.dataset.size);
                }
                return 0;
            });
            
            visibleSeries.forEach(s => container.appendChild(s));
        }
        
        function selectAll() {
            document.querySelectorAll('input[name="season"]').forEach(cb => {
                if (cb.closest('.series').style.display !== 'none') {
                    cb.checked = true;
                }
            });
        }
        
        function deselectAll() {
            document.querySelectorAll('input[name="season"]:checked').forEach(cb => cb.checked = false);
        }
        
        function deleteSelected() {
            const checked = document.querySelectorAll('input[name="season"]:checked');
            if (checked.length === 0) {
                alert('Please select at least one season to delete');
                return;
            }
            
            if (!confirm('Delete ' + checked.length + ' season(s)? This cannot be undone!')) {
                return;
            }
            
            const ids = Array.from(checked).map(cb => cb.value);
            
            fetch('/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ids: ids, type: 'season' })
            })
            .then(r => r.text())
            .then(msg => {
                alert(msg);
                location.reload();
            });
        }
    </script>
</body>
</html>`

var tvTmpl = template.Must(template.New("tv").Funcs(template.FuncMap{
	"formatTime": func(t time.Time) string {
		return t.Format("Jan 2, 2006 3:04 PM")
	},
}).Parse(tvTemplate))

func main() {
	http.HandleFunc("/", handleMovies)
	http.HandleFunc("/tv", handleTV)
	http.HandleFunc("/refresh", handleRefreshMovies)
	http.HandleFunc("/refresh-tv", handleRefreshTV)
	http.HandleFunc("/refresh-status", handleRefreshStatus)
	http.HandleFunc("/delete", handleDelete)
	
	fmt.Println("Server starting on :8080")
	http.ListenAndServe(":8080", nil)
}

func fetchAPI(request_type string, id string) ([]byte, error) {
	jellyfin_user_id := "470bcfb2d5db4f2fbadd795f49e2daf2"

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

	baseurl := "http://nas.home.arpa:8096/"
	url := baseurl + api

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "MediaBrowser Token=\"3ac377d146de4471aac66f330a7e2968\"")
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
	tmpl.Execute(w, data)
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
	if err := tvTmpl.Execute(w, data); err != nil {
		fmt.Println("Template error:", err)
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

func handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ids  []string `json:"ids"`
		Type string   `json:"type"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// TODO: Implement actual deletion logic when on server
	// For now, just acknowledge
	fmt.Printf("Delete request: type=%s, ids=%v\n", req.Type, req.Ids)
	w.Write([]byte(fmt.Sprintf("Delete function not yet implemented. Would delete %d %s(s)", len(req.Ids), req.Type)))
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