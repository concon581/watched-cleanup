package sonarr

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

// SearchByPath searches for a series in Sonarr by file path
func SearchByPath(client *http.Client, filePath string) (*models.SonarrSeries, int, error) {
	baseurl := os.Getenv("SONARR_BASE_URL")
	apiKey := os.Getenv("SONARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		return nil, 0, fmt.Errorf("Sonarr not configured")
	}

	// Ensure baseurl ends with /
	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	// Get all series - API does not support filtering by path
	// Must fetch all and check episode files for path match
	url := fmt.Sprintf("%sapi/v3/series", baseurl)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("Sonarr API returned HTTP %d", resp.StatusCode)
	}

	var seriesList []models.SonarrSeries
	if err := json.NewDecoder(resp.Body).Decode(&seriesList); err != nil {
		return nil, 0, err
	}

	// Search for episode file matching the path
	for _, series := range seriesList {
		// Get episode files for this series
		episodeFilesUrl := fmt.Sprintf("%sapi/v3/episodefile?seriesId=%d", baseurl, series.Id)
		episodeFilesReq, err := http.NewRequest("GET", episodeFilesUrl, nil)
		if err != nil {
			fmt.Printf("watched-cleanup: Error creating Sonarr episode files request for series %d: %v\n", series.Id, err)
			continue
		}
		episodeFilesReq.Header.Set("X-Api-Key", apiKey)
		episodeFilesResp, err := client.Do(episodeFilesReq)
		if err != nil {
			fmt.Printf("watched-cleanup: Error fetching Sonarr episode files for series %d: %v\n", series.Id, err)
			continue
		}

		var episodeFiles []models.SonarrEpisodeFile
		if err := json.NewDecoder(episodeFilesResp.Body).Decode(&episodeFiles); err != nil {
			episodeFilesResp.Body.Close()
			fmt.Printf("watched-cleanup: Error decoding Sonarr episode files for series %d: %v\n", series.Id, err)
			continue
		}
		episodeFilesResp.Body.Close()

		// Check if any episode file path matches
		// Normalize paths for comparison (handle case, trailing slashes, etc.)
		normalizedFilePath := filepath.Clean(filePath)
		for _, ef := range episodeFiles {
			normalizedEpisodePath := filepath.Clean(ef.Path)
			if normalizedEpisodePath == normalizedFilePath {
				// Get full series data with seasons
				seriesUrl := fmt.Sprintf("%sapi/v3/series/%d", baseurl, series.Id)
				seriesReq, err := http.NewRequest("GET", seriesUrl, nil)
				if err != nil {
					fmt.Printf("watched-cleanup: Error creating Sonarr series request for series %d: %v\n", series.Id, err)
					continue
				}
				seriesReq.Header.Set("X-Api-Key", apiKey)
				seriesResp, err := client.Do(seriesReq)
				if err != nil {
					fmt.Printf("watched-cleanup: Error fetching Sonarr series %d: %v\n", series.Id, err)
					continue
				}

				var fullSeries models.SonarrSeries
				if err := json.NewDecoder(seriesResp.Body).Decode(&fullSeries); err != nil {
					seriesResp.Body.Close()
					fmt.Printf("watched-cleanup: Error decoding Sonarr series %d: %v\n", series.Id, err)
					continue
				}
				seriesResp.Body.Close()

				return &fullSeries, ef.SeasonNumber, nil
			}
		}
	}

	return nil, 0, fmt.Errorf("series not found in Sonarr by path")
}

// SearchByTitle searches for a series in Sonarr by title
func SearchByTitle(client *http.Client, title string, seasonNumber int) (*models.SonarrSeries, error) {
	baseurl := os.Getenv("SONARR_BASE_URL")
	apiKey := os.Getenv("SONARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		return nil, fmt.Errorf("Sonarr not configured")
	}

	// Ensure baseurl ends with /
	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	// NOTE: Sonarr API v3 GET /api/v3/series does not support query parameters for filtering
	// Must fetch all series and filter client-side by title
	// Reference: https://wiki.servarr.com/sonarr/api
	url := fmt.Sprintf("%sapi/v3/series", baseurl)
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
		return nil, fmt.Errorf("Sonarr API returned HTTP %d", resp.StatusCode)
	}

	var seriesList []models.SonarrSeries
	if err := json.NewDecoder(resp.Body).Decode(&seriesList); err != nil {
		return nil, err
	}

	// Find exact match by title
	// Note: The series list from /api/v3/series already includes the seasons array,
	// so we can return it directly without an extra fetch
	for i := range seriesList {
		if strings.EqualFold(seriesList[i].Title, title) {
			return &seriesList[i], nil
		}
	}

	return nil, fmt.Errorf("series not found in Sonarr by title")
}

// UnmonitorSeason unmonitors a specific season in Sonarr
func UnmonitorSeason(client *http.Client, seriesId int, seasonNumber int) error {
	baseurl := os.Getenv("SONARR_BASE_URL")
	apiKey := os.Getenv("SONARR_API_KEY")
	if baseurl == "" || apiKey == "" {
		return fmt.Errorf("Sonarr not configured")
	}

	// Ensure baseurl ends with /
	if !strings.HasSuffix(baseurl, "/") {
		baseurl += "/"
	}

	// Get current series data
	url := fmt.Sprintf("%sapi/v3/series/%d", baseurl, seriesId)
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
		return fmt.Errorf("Sonarr API returned HTTP %d", resp.StatusCode)
	}

	// Read the full JSON response to preserve all fields
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Parse into a map to preserve all fields, then update monitored for the season
	var seriesData map[string]interface{}
	if err := json.Unmarshal(body, &seriesData); err != nil {
		return err
	}

	// Update monitored status for the specific season
	seasons, ok := seriesData["seasons"].([]interface{})
	if !ok {
		return fmt.Errorf("seasons field not found or invalid in series %d", seriesId)
	}

	found := false
	for _, seasonInterface := range seasons {
		season, ok := seasonInterface.(map[string]interface{})
		if !ok {
			continue
		}
		seasonNum, ok := season["seasonNumber"].(float64)
		if !ok {
			continue
		}
		if int(seasonNum) == seasonNumber {
			season["monitored"] = false
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("season %d not found in series %d", seasonNumber, seriesId)
	}

	// Update series
	updateUrl := fmt.Sprintf("%sapi/v3/series/%d", baseurl, seriesId)
	jsonData, err := json.Marshal(seriesData)
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
		fmt.Printf("watched-cleanup: Successfully unmonitored season %d in series %d in Sonarr\n", seasonNumber, seriesId)
		return nil
	}

	return fmt.Errorf("Sonarr unmonitor returned HTTP %d", updateResp.StatusCode)
}
