package radarr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/concon581/watched-cleanup/models"
)

// Reference: https://radarr.video/docs/api/
func SearchByPath(client *http.Client, filePath string) (*models.RadarrMovie, error) {
	baseurl := os.Getenv("RADARR_BASE_URL")
	apiKey := os.Getenv("RADARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		return nil, fmt.Errorf("Radarr not configured")
	}

	// Ensure baseurl ends with /
	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	// Get all movies - API does not support filtering by path
	// Must fetch all and check movie files for path match
	url := fmt.Sprintf("%sapi/v3/movie", baseurl)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Radarr API returned HTTP %d", resp.StatusCode)
	}

	var movies []models.RadarrMovie
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, err
	}

	// Search for movie with matching file path
	for _, movie := range movies {
		// Get movie files to check paths
		movieFilesUrl := fmt.Sprintf("%sapi/v3/moviefile?movieId=%d", baseurl, movie.Id)
		movieFilesReq, err := http.NewRequest("GET", movieFilesUrl, nil)
		if err != nil {
			fmt.Printf("watched-cleanup: Error creating Radarr movie files request for movie %d: %v\n", movie.Id, err)
			continue
		}
		movieFilesReq.Header.Set("X-Api-Key", apiKey)
		movieFilesResp, err := client.Do(movieFilesReq)
		if err != nil {
			fmt.Printf("watched-cleanup: Error fetching Radarr movie files for movie %d: %v\n", movie.Id, err)
			continue
		}

		var movieFiles []models.RadarrMovieFile
		if err := json.NewDecoder(movieFilesResp.Body).Decode(&movieFiles); err != nil {
			movieFilesResp.Body.Close()
			fmt.Printf("watched-cleanup: Error decoding Radarr movie files for movie %d: %v\n", movie.Id, err)
			continue
		}
		movieFilesResp.Body.Close()

		// Check if any movie file path matches
		// Normalize paths for comparison (handle case, trailing slashes, etc.)
		normalizedFilePath := filepath.Clean(filePath)
		for _, mf := range movieFiles {
			normalizedMoviePath := filepath.Clean(mf.Path)
			if normalizedMoviePath == normalizedFilePath {
				return &movie, nil
			}
		}
	}

	return nil, fmt.Errorf("movie not found in Radarr by path")
}

func SearchByTitle(client *http.Client, title string, year int) (*models.RadarrMovie, error) {
	baseurl := os.Getenv("RADARR_BASE_URL")
	apiKey := os.Getenv("RADARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		return nil, fmt.Errorf("Radarr not configured")
	}

	// Ensure baseurl ends with /
	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	// NOTE: Radarr API v3 GET /api/v3/movie does not support query parameters for filtering
	// Must fetch all movies and filter client-side by title and year
	// Reference: https://radarr.video/docs/api/
	url := fmt.Sprintf("%sapi/v3/movie", baseurl)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Radarr API returned HTTP %d", resp.StatusCode)
	}

	var movies []models.RadarrMovie
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, err
	}

	// Find exact match by title and year
	for i := range movies {
		if strings.EqualFold(movies[i].Title, title) && movies[i].Year == year {
			return &movies[i], nil
		}
	}

	return nil, fmt.Errorf("movie not found in Radarr by title and year")
}

func UnmonitorMovie(client *http.Client, movieId int) error {
	baseurl := os.Getenv("RADARR_BASE_URL")
	apiKey := os.Getenv("RADARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		return fmt.Errorf("Radarr not configured")
	}

	// Ensure baseurl ends with /
	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	// Get current movie data
	url := fmt.Sprintf("%sapi/v3/movie/%d", baseurl, movieId)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("Radarr API returned HTTP %d", resp.StatusCode)
	}

	// Read the full JSON response to preserve all fields
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Parse into a map to preserve all fields, then update monitored
	var movieData map[string]interface{}
	if err := json.Unmarshal(body, &movieData); err != nil {
		return err
	}

	// Update monitored status
	movieData["monitored"] = false

	// Update movie
	updateUrl := fmt.Sprintf("%sapi/v3/movie/%d", baseurl, movieId)
	jsonData, err := json.Marshal(movieData)
	if err != nil {
		return err
	}

	updateReq, err := http.NewRequest("PUT", updateUrl, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	updateReq.Header.Set("X-Api-Key", apiKey)
	updateReq.Header.Set("Content-Type", "application/json")

	updateResp, err := client.Do(updateReq)
	if err != nil {
		return err
	}
	defer updateResp.Body.Close()

	if updateResp.StatusCode >= 200 && updateResp.StatusCode < 300 {
		fmt.Printf("watched-cleanup: Successfully unmonitored movie %d in Radarr\n", movieId)
		return nil
	}

	return fmt.Errorf("Radarr unmonitor returned HTTP %d", updateResp.StatusCode)
}
