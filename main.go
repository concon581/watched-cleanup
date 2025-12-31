package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
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
}

var htmlTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Test Title</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .movie { margin-bottom: 20px; border: 1px solid #ccc; padding: 10px; }
        img { max-width: 200px; height: auto; }
    </style>
</head>
<body>
    <h1>Watched Movies</h1>
    {{range .Items}}
    <div class="movie">
        <h2>{{.Name}} {{if .ProductionYear}}({{.ProductionYear}}){{end}}</h2>
        <p>{{.Overview}}</p>
        <p>Size: {{printf "%.2f" .SizeGB}} GB</p>
        <p>Path: {{.Path}}</p>
        {{if .ImageTags.Primary}}
        <img src="http://nas.home.arpa:8096/Items/{{.Id}}/Images/Primary?maxWidth=200" alt="{{.Name}}">
        {{end}}
    </div>
    {{end}}
</body>
</html>`

var tmpl = template.Must(template.New("movies").Parse(htmlTemplate))

var tvTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Watched TV Shows</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        .series { margin-bottom: 20px; border: 1px solid #ccc; padding: 10px; }
        .season { margin-left: 20px; }
        img { max-width: 200px; height: auto; }
    </style>
</head>
<body>
    <h1>Watched TV Shows</h1>
    {{range .}}
    <div class="series">
        <h2>{{.Name}}</h2>
        {{if .ImageTags.Primary}}
        <img src="http://nas.home.arpa:8096/Items/{{.Id}}/Images/Primary?maxWidth=200" alt="{{.Name}}">
        {{end}}
        {{range .Seasons}}
        <div class="season">
            <h3>Season {{.SeasonNumber}}: {{.WatchedCount}}/{{.TotalCount}} episodes watched ({{printf "%.2f" .SizeGB}} GB)</h3>
        </div>
        {{end}}
    </div>
    {{end}}
</body>
</html>`

var tvTmpl = template.Must(template.New("tv").Parse(tvTemplate))

func main() {
	http.HandleFunc("/", handleMovies)
	http.HandleFunc("/tv", handleTV)
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
	}

	baseurl := "http://nas.home.arpa:8096/"
	url := baseurl + api
	fmt.Println("Fetching URL:", url)

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

func handleMovies(w http.ResponseWriter, r *http.Request) {
	body, err := fetchAPI("played_movies", "")
	if err != nil {
		http.Error(w, "Failed to fetch movies: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var movieList MovieList
	if err := json.Unmarshal(body, &movieList); err != nil {
		http.Error(w, "Failed to parse JSON: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Println("Number of movies:", len(movieList.Items))

	// Fetch size for each movie
	for i := range movieList.Items {
		fmt.Println("Fetching details for", movieList.Items[i].Id)
		detailsBody, err := fetchAPI("movie_details", movieList.Items[i].Id)
		if err != nil {
			fmt.Println("Error fetching details:", err)
			continue
		}
		fmt.Println("Details body length:", len(detailsBody))
		var details MovieDetails
		if err := json.Unmarshal(detailsBody, &details); err != nil {
			fmt.Println("Error unmarshaling details:", err)
			continue
		}
		fmt.Println("MediaSources count:", len(details.MediaSources))
		if len(details.MediaSources) > 0 {
			movieList.Items[i].Size = details.MediaSources[0].Size
			movieList.Items[i].SizeGB = float64(details.MediaSources[0].Size) / (1024 * 1024 * 1024)
			movieList.Items[i].Path = details.MediaSources[0].Path
			fmt.Println("Size set to:", details.MediaSources[0].Size)
		} else {
			fmt.Println("No MediaSources")
		}
	}

	// Render the HTML template
	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, movieList); err != nil {
		http.Error(w, "Failed to render template: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

func handleTV(w http.ResponseWriter, r *http.Request) {
	body, err := fetchAPI("watched_episodes", "")
	if err != nil {
		http.Error(w, "Failed to fetch episodes: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var episodeList EpisodeList
	if err := json.Unmarshal(body, &episodeList); err != nil {
		http.Error(w, "Failed to parse JSON: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Println("Unmarshaled", len(episodeList.Items), "episodes")

	// Group episodes by series and season
	grouped := make(map[string]*Series)
	
	for _, ep := range episodeList.Items {
		seriesId := ep.SeriesId
		if grouped[seriesId] == nil {
			grouped[seriesId] = &Series{
				Name:      ep.SeriesName,
				Id:        seriesId,
				ImageTags: make(map[string]string),
			}
		}
		seasonNum := ep.ParentIndexNumber
		// Find or add season
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
		}
	}

	// Fetch total counts and sizes for each season
	for _, series := range grouped {
		for i := range series.Seasons {
			seasonInfoBody, err := fetchAPI("season_info", series.Seasons[i].SeasonId)
			if err != nil {
				fmt.Println("Error fetching season info:", err)
				continue
			}
			var seasonInfo SeasonDetails
			if err := json.Unmarshal(seasonInfoBody, &seasonInfo); err != nil {
				fmt.Println("Error unmarshaling season info:", err)
				continue
			}
			series.Seasons[i].TotalCount = seasonInfo.ChildCount
			fmt.Printf("Season %d: ChildCount = %d\n", series.Seasons[i].SeasonNumber, seasonInfo.ChildCount)
			
			// Fetch ALL episodes in this season to get total size
			seasonEpisodesBody, err := fetchAPI("season_episodes", series.Seasons[i].SeasonId)
			if err != nil {
				fmt.Println("Error fetching season episodes:", err)
				continue
			}
			var seasonEpisodes EpisodeList
			if err := json.Unmarshal(seasonEpisodesBody, &seasonEpisodes); err != nil {
				fmt.Println("Error unmarshaling season episodes:", err)
				continue
			}
			
			// Calculate total size for all episodes in season
			var totalSize int64
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
					totalSize += details.MediaSources[0].Size
				}
			}
			series.Seasons[i].SizeGB = float64(totalSize) / (1024 * 1024 * 1024)
			fmt.Printf("Season %d: Total size = %.2f GB\n", series.Seasons[i].SeasonNumber, series.Seasons[i].SizeGB)
		}
	}

	var seriesList []Series
	for _, s := range grouped {
		seriesList = append(seriesList, *s)
	}

	// Render the TV template
	w.Header().Set("Content-Type", "text/html")
	if err := tvTmpl.Execute(w, seriesList); err != nil {
		http.Error(w, "Failed to render template: "+err.Error(), http.StatusInternalServerError)
		return
	}
}