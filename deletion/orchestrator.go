package deletion

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/concon581/watched-cleanup/filesystem"
	"github.com/concon581/watched-cleanup/jellyfin"
	"github.com/concon581/watched-cleanup/models"
	"github.com/concon581/watched-cleanup/radarr"
	"github.com/concon581/watched-cleanup/sonarr"
)

// UpdateProgress updates the delete progress tracking structure
func UpdateProgress(progressPtr *models.DeleteProgress, mutex *sync.RWMutex, current, total int, message, currentItem string) {
	mutex.Lock()
	progressPtr.Current = current
	progressPtr.Total = total
	progressPtr.Message = message
	progressPtr.CurrentItem = currentItem
	if total > 0 {
		progressPtr.Percent = int(float64(current) / float64(total) * 100)
	} else {
		progressPtr.Percent = 0
	}
	mutex.Unlock()
}

// UpdateProgressStages updates the stage-specific progress tracking
func UpdateProgressStages(progressPtr *models.DeleteProgress, mutex *sync.RWMutex, stageJellyfin, stageInode, stageRadarrSonarr string, episodeCurrent, episodeTotal int, errors []string) {
	mutex.Lock()
	progressPtr.StageJellyfin = stageJellyfin
	progressPtr.StageInode = stageInode
	progressPtr.StageRadarrSonarr = stageRadarrSonarr
	progressPtr.EpisodeCurrent = episodeCurrent
	progressPtr.EpisodeTotal = episodeTotal
	progressPtr.StageErrors = errors
	mutex.Unlock()
}

// PerformDelete executes the deletion process for movies or seasons
func PerformDelete(client *http.Client, ids []string, deleteType string, dryRun bool, progressPtr *models.DeleteProgress, resultPtr *models.DeleteResult, cacheMutex *sync.RWMutex, cachedMovies *models.MovieList, cachedSeries *[]models.Series, deleteMutex *sync.RWMutex, isDeletingPtr *bool) {
	mode := "deletion"
	if dryRun {
		mode = "TEST MODE (dry-run)"
	}
	fmt.Printf("watched-cleanup: Starting %s of %d %s(s)\n", mode, len(ids), deleteType)

	defer func() {
		deleteMutex.Lock()
		*isDeletingPtr = false
		deleteMutex.Unlock()
		if dryRun {
			fmt.Printf("watched-cleanup: Test mode completed - no files were actually deleted\n")
		} else {
			fmt.Printf("watched-cleanup: Deletion process completed\n")
		}
	}()

	var globalErrors []string
	var deletedItems []models.DeletedItem
	var totalSizeGB float64

	// Get hardlink search directory from env, default to /data/torrents for Docker
	hardlinkSearchDir := os.Getenv("TORRENTS_PATH")
	if hardlinkSearchDir == "" {
		hardlinkSearchDir = "/data/torrents"
	}
	fmt.Printf("watched-cleanup: Using hardlink search directory: %s\n", hardlinkSearchDir)

	for i, id := range ids {
		var filesToCheck []string
		var itemName string
		var itemSizeGB float64
		var episodeTotal int
		var episodeInodeComplete int
		var stageErrors []string

		// Initialize item tracking
		currentItem := models.DeletedItem{
			Type:             deleteType,
			HardlinksDeleted: []string{},
			Errors:           []string{},
			StageResults: models.StageResults{
				Inode:        models.StageResult{Status: "pending"},
				RadarrSonarr: models.StageResult{Status: "pending"},
				Jellyfin:     models.StageResult{Status: "pending"},
			},
		}

		// Handle different types differently
		if deleteType == "season" {
			modePrefix := ""
			if dryRun {
				modePrefix = "[TEST] "
			}
			fmt.Printf("watched-cleanup: Processing season %d/%d (ID: %s)\n", i+1, len(ids), id)
			UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), fmt.Sprintf("%sProcessing season %d of %d", modePrefix, i+1, len(ids)), "")
			UpdateProgressStages(progressPtr, deleteMutex, "pending", "pending", "pending", 0, 0, []string{})

			seasonEpisodesBody, err := jellyfin.FetchAPI(client, "season_episodes", id)
			if err != nil {
				errMsg := fmt.Sprintf("Error fetching season episodes: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				globalErrors = append(globalErrors, errMsg)
				UpdateProgressStages(progressPtr, deleteMutex, "error", "pending", "pending", 0, 0, []string{errMsg})
				continue
			}

			var seasonEpisodes models.EpisodeList
			if err := json.Unmarshal(seasonEpisodesBody, &seasonEpisodes); err != nil {
				errMsg := fmt.Sprintf("Error parsing season episodes: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				globalErrors = append(globalErrors, errMsg)
				UpdateProgressStages(progressPtr, deleteMutex, "error", "pending", "pending", 0, 0, []string{errMsg})
				continue
			}

			fmt.Printf("watched-cleanup: Found %d episodes in season\n", len(seasonEpisodes.Items))

			// Get season name and size
			cacheMutex.RLock()
			for _, series := range *cachedSeries {
				for _, season := range series.Seasons {
					if season.SeasonId == id {
						itemName = fmt.Sprintf("%s - Season %d", series.Name, season.SeasonNumber)
						itemSizeGB = season.SizeGB
						break
					}
				}
			}
			cacheMutex.RUnlock()
			fmt.Printf("watched-cleanup: Season name: %s (%.2f GB)\n", itemName, itemSizeGB)

			// Initialize episode tracking
			episodeTotal = len(seasonEpisodes.Items)
			episodeInodeComplete = 0
			stageErrors = []string{}

			// Collect all episode files first
			for _, ep := range seasonEpisodes.Items {
				episodeDetailsBody, err := jellyfin.FetchAPI(client, "episode_details", ep.Id)
				if err != nil {
					fmt.Printf("watched-cleanup: Error fetching episode %s: %v\n", ep.Id, err)
					continue
				}
				var details models.MovieDetails
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

			// Process each episode through all 3 stages
			UpdateProgressStages(progressPtr, deleteMutex, "pending", "processing", "pending", 0, episodeTotal, []string{})
		} else if deleteType == "movie" {
			modePrefix := ""
			if dryRun {
				modePrefix = "[TEST] "
			}
			episodeTotal = 0
			stageErrors = []string{}
			fmt.Printf("watched-cleanup: Processing movie %d/%d (ID: %s)\n", i+1, len(ids), id)
			UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), fmt.Sprintf("%sProcessing movie %d of %d", modePrefix, i+1, len(ids)), "")
			UpdateProgressStages(progressPtr, deleteMutex, "pending", "pending", "pending", 0, 0, []string{})

			detailsBody, err := jellyfin.FetchAPI(client, "movie_details", id)
			if err != nil {
				errMsg := fmt.Sprintf("Error fetching movie details: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				globalErrors = append(globalErrors, errMsg)
				continue
			}

			var details models.MovieDetails
			if err := json.Unmarshal(detailsBody, &details); err != nil {
				errMsg := fmt.Sprintf("Error parsing movie details: %v", err)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				globalErrors = append(globalErrors, errMsg)
				continue
			}

			if len(details.MediaSources) > 0 {
				path := details.MediaSources[0].Path
				if path != "" {
					filesToCheck = append(filesToCheck, path)
					fmt.Printf("watched-cleanup: Movie file: %s\n", path)
				}

				// Get movie name and size
				cacheMutex.RLock()
				for _, movie := range cachedMovies.Items {
					if movie.Id == id {
						itemName = movie.Name
						itemSizeGB = movie.SizeGB
						break
					}
				}
				cacheMutex.RUnlock()
				fmt.Printf("watched-cleanup: Movie name: %s (%.2f GB)\n", itemName, itemSizeGB)
			}
		}

		// Find and delete hardlinks - Stage 1: Inode/Hardlink matching
		currentItem.StageResults.Inode.Status = "processing"
		currentItem.StageResults.Inode.Details = []string{}
		filesDeleted := 0

		if len(filesToCheck) > 0 {
			fmt.Printf("watched-cleanup: Checking %d file(s) for hardlinks\n", len(filesToCheck))
			episodeIndex := 0
			for _, filePath := range filesToCheck {
				if deleteType == "season" {
					episodeIndex++
					UpdateProgressStages(progressPtr, deleteMutex, "pending", "processing", "pending", episodeIndex, episodeTotal, stageErrors)
					UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), fmt.Sprintf("Stage 1: Processing episode %d/%d - Inode/Hardlink", episodeIndex, episodeTotal), filepath.Base(filePath))
				} else {
					progressMsg := fmt.Sprintf("Stage 1: Deleting files for %s", itemName)
					if dryRun {
						progressMsg = fmt.Sprintf("[TEST] Stage 1: Would delete files for %s", itemName)
					}
					UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), progressMsg, filepath.Base(filePath))
					UpdateProgressStages(progressPtr, deleteMutex, "pending", "processing", "pending", 0, 0, []string{})
				}
				fmt.Printf("watched-cleanup: Processing file: %s\n", filePath)

				if _, err := os.Stat(filePath); os.IsNotExist(err) {
					fmt.Printf("watched-cleanup: File doesn't exist (already deleted?): %s\n", filePath)
					continue
				}

				// Check if hardlink search directory exists
				if _, err := os.Stat(hardlinkSearchDir); os.IsNotExist(err) {
					fmt.Printf("watched-cleanup: Hardlink search directory doesn't exist, skipping hardlink check: %s\n", hardlinkSearchDir)
				} else {
					hardlinks, err := filesystem.FindHardlinks(filePath, hardlinkSearchDir)
					if err != nil {
						errMsg := fmt.Sprintf("Error finding hardlinks for %s: %v", filePath, err)
						fmt.Printf("watched-cleanup: %s\n", errMsg)
						currentItem.Errors = append(currentItem.Errors, errMsg)
						globalErrors = append(globalErrors, errMsg)
					} else {
						if len(hardlinks) > 0 {
							fmt.Printf("watched-cleanup: Found %d hardlink(s) for %s\n", len(hardlinks), filepath.Base(filePath))
							for _, link := range hardlinks {
								if dryRun {
									fmt.Printf("watched-cleanup: [DRY-RUN] Would delete hardlink: %s\n", link)
									currentItem.HardlinksDeleted = append(currentItem.HardlinksDeleted, link)
								} else {
									fmt.Printf("watched-cleanup: Deleting hardlink: %s\n", link)
									if err := os.Remove(link); err != nil {
										errMsg := fmt.Sprintf("Error deleting hardlink %s: %v", link, err)
										fmt.Printf("watched-cleanup: %s\n", errMsg)
										currentItem.Errors = append(currentItem.Errors, errMsg)
										globalErrors = append(globalErrors, errMsg)
									} else {
										currentItem.HardlinksDeleted = append(currentItem.HardlinksDeleted, link)
										fmt.Printf("watched-cleanup: Successfully deleted hardlink: %s\n", link)
									}
								}
							}
						} else {
							fmt.Printf("watched-cleanup: No hardlinks found for %s\n", filepath.Base(filePath))
						}
					}
				}

				// Delete the original file
				if dryRun {
					fmt.Printf("watched-cleanup: [DRY-RUN] Would delete original file: %s\n", filePath)
					filesDeleted++
				} else {
					fmt.Printf("watched-cleanup: Deleting original file: %s\n", filePath)
					if err := os.Remove(filePath); err != nil {
						errMsg := fmt.Sprintf("Error deleting %s: %v", filePath, err)
						fmt.Printf("watched-cleanup: %s\n", errMsg)
						currentItem.Errors = append(currentItem.Errors, errMsg)
						globalErrors = append(globalErrors, errMsg)
					} else {
						fmt.Printf("watched-cleanup: Successfully deleted file: %s\n", filePath)
						filesDeleted++
					}
				}
				// Mark episode as complete for inode stage (for seasons)
				if deleteType == "season" {
					episodeInodeComplete++
				}
			}
			currentItem.FilesDeleted = filesDeleted
			// Mark inode stage as complete after processing all files
			if deleteType == "season" {
				if episodeInodeComplete == episodeTotal {
					UpdateProgressStages(progressPtr, deleteMutex, "pending", "complete", "pending", episodeInodeComplete, episodeTotal, stageErrors)
					currentItem.StageResults.Inode.Status = "success"
					currentItem.StageResults.Inode.Message = fmt.Sprintf("Deleted %d files and %d hardlinks", filesDeleted, len(currentItem.HardlinksDeleted))
				} else {
					errMsg := fmt.Sprintf("Inode stage: Only %d/%d episodes completed", episodeInodeComplete, episodeTotal)
					stageErrors = append(stageErrors, errMsg)
					currentItem.Errors = append(currentItem.Errors, errMsg)
					UpdateProgressStages(progressPtr, deleteMutex, "pending", "error", "pending", episodeInodeComplete, episodeTotal, stageErrors)
					currentItem.StageResults.Inode.Status = "error"
					currentItem.StageResults.Inode.Message = errMsg
				}
			} else {
				// For movies, mark inode stage complete
				UpdateProgressStages(progressPtr, deleteMutex, "pending", "complete", "pending", 0, 0, []string{})
				currentItem.StageResults.Inode.Status = "success"
				currentItem.StageResults.Inode.Message = fmt.Sprintf("Deleted %d files and %d hardlinks", filesDeleted, len(currentItem.HardlinksDeleted))
			}
		} else {
			fmt.Printf("watched-cleanup: No files to delete for %s\n", itemName)
			if deleteType == "season" {
				UpdateProgressStages(progressPtr, deleteMutex, "pending", "error", "pending", 0, episodeTotal, []string{"No files found to delete"})
			} else {
				UpdateProgressStages(progressPtr, deleteMutex, "pending", "error", "pending", 0, 0, []string{"No files found to delete"})
			}
			currentItem.StageResults.Inode.Status = "error"
			currentItem.StageResults.Inode.Message = "No files found to delete"
		}

		// Stage 2: Unmonitor in Sonarr/Radarr before deleting from Jellyfin
		currentItem.StageResults.RadarrSonarr.Status = "processing"
		UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "processing", progressPtr.EpisodeCurrent, progressPtr.EpisodeTotal, progressPtr.StageErrors)
		if deleteType == "movie" {
			fmt.Printf("watched-cleanup: Attempting to unmonitor movie in Radarr: %s\n", itemName)
			progressMsg := fmt.Sprintf("Stage 2: Unmonitoring in Radarr: %s", itemName)
			if dryRun {
				progressMsg = fmt.Sprintf("[TEST] Stage 2: Would unmonitor in Radarr: %s", itemName)
			}
			UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), progressMsg, "")

			var radarrMovie *models.RadarrMovie
			var err error

			// Try file path search first
			if len(filesToCheck) > 0 {
				radarrMovie, err = radarr.SearchByPath(client, filesToCheck[0])
				if err != nil {
					fmt.Printf("watched-cleanup: Radarr path search failed: %v, trying title search\n", err)
				}
			}

			// Fallback to title/year search
			if radarrMovie == nil {
				cacheMutex.RLock()
				var movieYear int
				for _, movie := range cachedMovies.Items {
					if movie.Id == id {
						movieYear = movie.ProductionYear
						break
					}
				}
				cacheMutex.RUnlock()

				if itemName != "" && movieYear > 0 {
					radarrMovie, err = radarr.SearchByTitle(client, itemName, movieYear)
					if err != nil {
						fmt.Printf("watched-cleanup: Radarr title search failed: %v\n", err)
					}
				}
			}

			if radarrMovie != nil {
				if dryRun {
					fmt.Printf("watched-cleanup: [DRY-RUN] Would unmonitor movie %s (ID: %d) in Radarr\n", itemName, radarrMovie.Id)
					currentItem.StageResults.RadarrSonarr.Status = "success"
					currentItem.StageResults.RadarrSonarr.Message = fmt.Sprintf("Would unmonitor in Radarr (ID: %d)", radarrMovie.Id)
					UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "complete", 0, 0, []string{})
				} else {
					if err := radarr.UnmonitorMovie(client, radarrMovie.Id); err != nil {
						errMsg := fmt.Sprintf("Failed to unmonitor movie %s in Radarr: %v", itemName, err)
						fmt.Printf("watched-cleanup: %s\n", errMsg)
						currentItem.Errors = append(currentItem.Errors, errMsg)
						globalErrors = append(globalErrors, errMsg)
						stageErrors = append(stageErrors, errMsg)
						currentItem.StageResults.RadarrSonarr.Status = "error"
						currentItem.StageResults.RadarrSonarr.Message = fmt.Sprintf("Failed to unmonitor in Radarr")
						UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "error", 0, 0, stageErrors)
					} else {
						fmt.Printf("watched-cleanup: Successfully unmonitored movie %s in Radarr\n", itemName)
						currentItem.StageResults.RadarrSonarr.Status = "success"
						currentItem.StageResults.RadarrSonarr.Message = "Successfully unmonitored in Radarr"
						UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "complete", 0, 0, []string{})
					}
				}
			} else {
				errMsg := fmt.Sprintf("Movie %s not found in Radarr", itemName)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				currentItem.Errors = append(currentItem.Errors, errMsg)
				globalErrors = append(globalErrors, errMsg)
				stageErrors = append(stageErrors, errMsg)
				currentItem.StageResults.RadarrSonarr.Status = "error"
				currentItem.StageResults.RadarrSonarr.Message = "Not found in Radarr"
				UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "error", 0, 0, stageErrors)
			}
		} else if deleteType == "season" {
			fmt.Printf("watched-cleanup: Attempting to unmonitor season in Sonarr: %s\n", itemName)
			progressMsg := fmt.Sprintf("Stage 2: Unmonitoring in Sonarr: %s", itemName)
			if dryRun {
				progressMsg = fmt.Sprintf("[TEST] Stage 2: Would unmonitor in Sonarr: %s", itemName)
			}
			UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), progressMsg, "")

			var sonarrSeries *models.SonarrSeries
			var seasonNumber int
			var err error

			// Try file path search first
			if len(filesToCheck) > 0 {
				sonarrSeries, seasonNumber, err = sonarr.SearchByPath(client, filesToCheck[0])
				if err != nil {
					fmt.Printf("watched-cleanup: Sonarr path search failed: %v, trying title search\n", err)
				}
			}

			// Fallback to title/season search
			if sonarrSeries == nil {
				cacheMutex.RLock()
				var seriesName string
				var foundSeasonNumber int
				for _, series := range *cachedSeries {
					for _, season := range series.Seasons {
						if season.SeasonId == id {
							seriesName = series.Name
							foundSeasonNumber = season.SeasonNumber
							break
						}
					}
					if seriesName != "" {
						break
					}
				}
				cacheMutex.RUnlock()

				if seriesName != "" {
					sonarrSeries, err = sonarr.SearchByTitle(client, seriesName, foundSeasonNumber)
					if err != nil {
						fmt.Printf("watched-cleanup: Sonarr title search failed: %v\n", err)
					} else {
						seasonNumber = foundSeasonNumber
					}
				}
			}

			if sonarrSeries != nil {
				if dryRun {
					fmt.Printf("watched-cleanup: [DRY-RUN] Would unmonitor season %s (Series ID: %d, Season: %d) in Sonarr\n", itemName, sonarrSeries.Id, seasonNumber)
					currentItem.StageResults.RadarrSonarr.Status = "success"
					currentItem.StageResults.RadarrSonarr.Message = fmt.Sprintf("Would unmonitor in Sonarr (ID: %d, Season: %d)", sonarrSeries.Id, seasonNumber)
					UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "complete", episodeTotal, episodeTotal, []string{})
				} else {
					if err := sonarr.UnmonitorSeason(client, sonarrSeries.Id, seasonNumber); err != nil {
						errMsg := fmt.Sprintf("Failed to unmonitor season %s in Sonarr: %v", itemName, err)
						fmt.Printf("watched-cleanup: %s\n", errMsg)
						currentItem.Errors = append(currentItem.Errors, errMsg)
						globalErrors = append(globalErrors, errMsg)
						stageErrors = append(stageErrors, errMsg)
						currentItem.StageResults.RadarrSonarr.Status = "error"
						currentItem.StageResults.RadarrSonarr.Message = "Failed to unmonitor in Sonarr"
						UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "error", episodeTotal, episodeTotal, stageErrors)
					} else {
						fmt.Printf("watched-cleanup: Successfully unmonitored season %s in Sonarr\n", itemName)
						currentItem.StageResults.RadarrSonarr.Status = "success"
						currentItem.StageResults.RadarrSonarr.Message = "Successfully unmonitored in Sonarr"
						UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "complete", episodeTotal, episodeTotal, []string{})
					}
				}
			} else {
				errMsg := fmt.Sprintf("Season %s not found in Sonarr", itemName)
				fmt.Printf("watched-cleanup: %s\n", errMsg)
				currentItem.Errors = append(currentItem.Errors, errMsg)
				globalErrors = append(globalErrors, errMsg)
				stageErrors = append(stageErrors, errMsg)
				currentItem.StageResults.RadarrSonarr.Status = "error"
				currentItem.StageResults.RadarrSonarr.Message = "Not found in Sonarr"
				UpdateProgressStages(progressPtr, deleteMutex, "pending", progressPtr.StageInode, "error", episodeTotal, episodeTotal, stageErrors)
			}
		}

		// Stage 3: Delete from Jellyfin
		currentItem.StageResults.Jellyfin.Status = "processing"
		UpdateProgressStages(progressPtr, deleteMutex, "processing", progressPtr.StageInode, progressPtr.StageRadarrSonarr, progressPtr.EpisodeCurrent, progressPtr.EpisodeTotal, progressPtr.StageErrors)
		if dryRun {
			fmt.Printf("watched-cleanup: [DRY-RUN] Would delete from Jellyfin database: %s (%s)\n", id, itemName)
			progressMsg := fmt.Sprintf("[TEST] Stage 3: Would remove from Jellyfin: %s", itemName)
			if deleteType == "season" {
				progressMsg = fmt.Sprintf("[TEST] Stage 3: Would remove from Jellyfin: %s (%d/%d episodes)", itemName, episodeTotal, episodeTotal)
			}
			UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), progressMsg, "")
			currentItem.StageResults.Jellyfin.Status = "success"
			currentItem.StageResults.Jellyfin.Message = "Would remove from Jellyfin"
			UpdateProgressStages(progressPtr, deleteMutex, "complete", progressPtr.StageInode, progressPtr.StageRadarrSonarr, episodeTotal, episodeTotal, []string{})
		} else {
			fmt.Printf("watched-cleanup: Deleting from Jellyfin database: %s (%s)\n", id, itemName)
			progressMsg := fmt.Sprintf("Stage 3: Removing from Jellyfin: %s", itemName)
			if deleteType == "season" {
				progressMsg = fmt.Sprintf("Stage 3: Removing from Jellyfin: %s (%d/%d episodes)", itemName, episodeTotal, episodeTotal)
			}
			UpdateProgress(progressPtr, deleteMutex, i+1, len(ids), progressMsg, "")
			jellyfin.CallJellyfinDelete(client, id)
			fmt.Printf("watched-cleanup: Jellyfin delete request sent for: %s\n", id)
			currentItem.StageResults.Jellyfin.Status = "success"
			currentItem.StageResults.Jellyfin.Message = "Removed from Jellyfin"
			UpdateProgressStages(progressPtr, deleteMutex, "complete", progressPtr.StageInode, progressPtr.StageRadarrSonarr, episodeTotal, episodeTotal, []string{})
		}

		// Finalize item tracking
		currentItem.Name = itemName
		currentItem.SizeGB = itemSizeGB
		totalSizeGB += itemSizeGB
		deletedItems = append(deletedItems, currentItem)
	}

	// Update final result
	summaryType := "Deletion"
	if dryRun {
		summaryType = "TEST MODE (Dry-Run) - No files were actually deleted"
	}

	// Calculate total hardlinks
	totalHardlinks := 0
	for _, item := range deletedItems {
		totalHardlinks += len(item.HardlinksDeleted)
	}

	fmt.Printf("watched-cleanup: %s summary:\n", summaryType)
	if dryRun {
		fmt.Printf("watched-cleanup:   - Items that would be deleted: %d\n", len(ids))
		fmt.Printf("watched-cleanup:   - Hardlinks that would be deleted: %d\n", totalHardlinks)
		fmt.Printf("watched-cleanup:   - Total size: %.2f GB\n", totalSizeGB)
	} else {
		fmt.Printf("watched-cleanup:   - Items deleted: %d\n", len(ids))
		fmt.Printf("watched-cleanup:   - Hardlinks deleted: %d\n", totalHardlinks)
		fmt.Printf("watched-cleanup:   - Total size: %.2f GB\n", totalSizeGB)
	}
	fmt.Printf("watched-cleanup:   - Errors: %d\n", len(globalErrors))
	if len(globalErrors) > 0 {
		for _, err := range globalErrors {
			fmt.Printf("watched-cleanup:     * %s\n", err)
		}
	}

	deleteMutex.Lock()
	*resultPtr = models.DeleteResult{
		DeletedCount:     len(ids),
		DeletedHardlinks: totalHardlinks,
		Errors:           globalErrors,
		TotalSizeGB:      totalSizeGB,
		DryRun:           dryRun,
		Items:            deletedItems,
	}
	deleteMutex.Unlock()
}
