package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/concon581/watched-cleanup/models"
)

// FetchAPI performs a GET request to the Jellyfin API
func FetchAPI(client *http.Client, requestType string, id string) ([]byte, error) {
	jellyfinUserID := os.Getenv("JELLYFIN_USER_ID")

	var api string
	if requestType == "played_movies" {
		api = "Users/" + jellyfinUserID + "/Items?Recursive=true&IsPlayed=true&IncludeItemTypes=Movie&Fields=DateCreated"
	} else if requestType == "watched_episodes" {
		api = "Users/" + jellyfinUserID + "/Items?Recursive=true&IsPlayed=true&IncludeItemTypes=Episode&Fields=DateCreated"
	} else if requestType == "season_details" {
		api = "Items/" + id + "?userId=" + jellyfinUserID + "&Fields=DateCreated"
	} else if requestType == "season_info" {
		api = "Items/" + id + "?userId=" + jellyfinUserID + "&Fields=DateCreated"
	} else if requestType == "series_seasons" {
		api = "Shows/" + id + "/Seasons?userId=" + jellyfinUserID + "&Fields=DateCreated"
	} else if requestType == "movie_details" {
		api = "Users/" + jellyfinUserID + "/Items/" + id + "?Fields=MediaSources,DateCreated"
	} else if requestType == "episode_details" {
		api = "Users/" + jellyfinUserID + "/Items/" + id + "?Fields=MediaSources,DateCreated"
	} else if requestType == "season_episodes" {
		api = "Users/" + jellyfinUserID + "/Items?ParentId=" + id + "&Fields=MediaSources,DateCreated"
	} else if requestType == "series_details" {
		api = "Users/" + jellyfinUserID + "/Items/" + id + "?Fields=DateCreated"
	}

	baseurl := os.Getenv("JELLYFIN_BASE_URL")

	url := baseurl + api

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	token := os.Getenv("JELLYFIN_API_KEY")

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// CallJellyfinDelete sends a DELETE request to remove an item from Jellyfin
func CallJellyfinDelete(client *http.Client, id string) {
	baseurl := os.Getenv("JELLYFIN_BASE_URL")
	token := os.Getenv("JELLYFIN_API_KEY")
	url := fmt.Sprintf("%sItems/%s", baseurl, id)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		fmt.Printf("watched-cleanup: Error creating Jellyfin delete request: %v\n", err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	resp, err := client.Do(req)
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

// FetchMovieData retrieves and enriches movie data from Jellyfin
func FetchMovieData(client *http.Client, updateProgress func(current, total int, message string)) models.MovieList {
	body, err := FetchAPI(client, "played_movies", "")
	if err != nil {
		fmt.Println("Error fetching movies:", err)
		return models.MovieList{}
	}

	var movieList models.MovieList
	if err := json.Unmarshal(body, &movieList); err != nil {
		fmt.Println("Error parsing movies:", err)
		return models.MovieList{}
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

			detailsBody, err := FetchAPI(client, "movie_details", movieList.Items[idx].Id)
			if err != nil {
				atomic.AddInt32(&completed, 1)
				return
			}
			var details models.MovieDetails
			if err := json.Unmarshal(detailsBody, &details); err != nil {
				atomic.AddInt32(&completed, 1)
				return
			}
			mu.Lock()
			if len(details.MediaSources) > 0 {
				movieList.Items[idx].Size = details.MediaSources[0].Size
				movieList.Items[idx].SizeGB = float64(details.MediaSources[0].Size) / (1024 * 1024 * 1024)
				movieList.Items[idx].Path = details.MediaSources[0].Path
			}
			// Parse DateCreated from details (preferred) or initial list
			dateStr := details.DateCreated
			if dateStr == "" {
				dateStr = movieList.Items[idx].DateCreated
			}
			if dateStr != "" {
				if dateAdded, err := time.Parse("2006-01-02T15:04:05.0000000Z", dateStr); err == nil {
					movieList.Items[idx].DateAdded = dateAdded
				} else if dateAdded, err := time.Parse("2006-01-02T15:04:05Z", dateStr); err == nil {
					movieList.Items[idx].DateAdded = dateAdded
				} else if dateAdded, err := time.Parse("2006-01-02T15:04:05", dateStr); err == nil {
					movieList.Items[idx].DateAdded = dateAdded
				}
			}
			mu.Unlock()

			current := atomic.AddInt32(&completed, 1)
			if updateProgress != nil {
				updateProgress(int(current), len(movieList.Items), fmt.Sprintf("Fetching movie %d/%d", int(current), len(movieList.Items)))
			}
		}(i)
	}

	wg.Wait()
	return movieList
}

// FetchTVData retrieves and enriches TV series data from Jellyfin
func FetchTVData(client *http.Client, updateProgress func(current, total int, message string)) []models.Series {
	body, err := FetchAPI(client, "watched_episodes", "")
	if err != nil {
		fmt.Println("Error fetching episodes:", err)
		return nil
	}

	var episodeList models.EpisodeList
	if err := json.Unmarshal(body, &episodeList); err != nil {
		fmt.Println("Error parsing episodes:", err)
		return nil
	}

	fmt.Println("Unmarshaled", len(episodeList.Items), "episodes")

	grouped := make(map[string]*models.Series)

	for _, ep := range episodeList.Items {
		seriesId := ep.SeriesId
		if grouped[seriesId] == nil {
			// Fetch series details to get artwork
			seriesBody, err := FetchAPI(client, "series_details", seriesId)
			if err == nil {
				var seriesDetails struct {
					ImageTags map[string]string `json:"ImageTags"`
				}
				json.Unmarshal(seriesBody, &seriesDetails)
				grouped[seriesId] = &models.Series{
					Name:      ep.SeriesName,
					Id:        seriesId,
					ImageTags: seriesDetails.ImageTags,
				}
			} else {
				grouped[seriesId] = &models.Series{
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
			grouped[seriesId].Seasons = append(grouped[seriesId].Seasons, models.SeasonInfo{
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
		go func(s *models.Series) {
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

					seasonInfoBody, err := FetchAPI(client, "season_info", s.Seasons[idx].SeasonId)
					if err != nil {
						fmt.Println("    Error fetching season info:", err)
						return
					}
					var seasonInfo models.SeasonDetails
					if err := json.Unmarshal(seasonInfoBody, &seasonInfo); err != nil {
						fmt.Println("    Error unmarshaling season info:", err)
						return
					}
					s.Seasons[idx].TotalCount = seasonInfo.ChildCount
					// Parse season DateAdded if available
					if seasonInfo.DateCreated != "" {
						if dateAdded, err := time.Parse("2006-01-02T15:04:05.0000000Z", seasonInfo.DateCreated); err == nil {
							s.Seasons[idx].DateAdded = dateAdded
						} else if dateAdded, err := time.Parse("2006-01-02T15:04:05Z", seasonInfo.DateCreated); err == nil {
							s.Seasons[idx].DateAdded = dateAdded
						}
					}
					fmt.Printf("    Season %d: %d episodes total\n", s.Seasons[idx].SeasonNumber, seasonInfo.ChildCount)

					seasonEpisodesBody, err := FetchAPI(client, "season_episodes", s.Seasons[idx].SeasonId)
					if err != nil {
						fmt.Println("    Error fetching season episodes:", err)
						return
					}
					var seasonEpisodes models.EpisodeList
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
					var earliestDate time.Time
					var dateMu sync.Mutex
					hasDate := false

					for _, ep := range seasonEpisodes.Items {
						epWg.Add(1)
						go func(episode models.Episode) {
							defer epWg.Done()
							epSem <- struct{}{}
							defer func() { <-epSem }()

							episodeDetailsBody, err := FetchAPI(client, "episode_details", episode.Id)
							if err != nil {
								return
							}
							var details models.MovieDetails
							if err := json.Unmarshal(episodeDetailsBody, &details); err != nil {
								return
							}
							if len(details.MediaSources) > 0 {
								sizeMu.Lock()
								totalSize += details.MediaSources[0].Size
								sizeMu.Unlock()
							}
							// Capture DateAdded from episode
							if details.DateCreated != "" {
								var dateAdded time.Time
								if parsed, err := time.Parse("2006-01-02T15:04:05.0000000Z", details.DateCreated); err == nil {
									dateAdded = parsed
								} else if parsed, err := time.Parse("2006-01-02T15:04:05Z", details.DateCreated); err == nil {
									dateAdded = parsed
								} else {
									return
								}
								dateMu.Lock()
								if !hasDate || dateAdded.Before(earliestDate) {
									earliestDate = dateAdded
									hasDate = true
								}
								dateMu.Unlock()
							}
						}(ep)
					}

					epWg.Wait()
					s.Seasons[idx].SizeGB = float64(totalSize) / (1024 * 1024 * 1024)
					// Use earliest episode date if season date not available
					if hasDate && s.Seasons[idx].DateAdded.IsZero() {
						s.Seasons[idx].DateAdded = earliestDate
					}

					mu.Lock()
					s.TotalSize += s.Seasons[idx].SizeGB
					mu.Unlock()

					fmt.Printf("    Season %d: %.2f GB total\n", s.Seasons[idx].SeasonNumber, s.Seasons[idx].SizeGB)
				}(i)
			}

			seasonWg.Wait()

			// Update progress after series completes
			current := atomic.AddInt32(&seriesCompleted, 1)
			if updateProgress != nil {
				updateProgress(int(current), totalSeries, fmt.Sprintf("Completed %d/%d series", int(current), totalSeries))
			}
		}(series)
	}

	wg.Wait()

	var seriesList []models.Series
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
