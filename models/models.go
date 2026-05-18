package models

import "time"

// Movie represents a movie from Jellyfin
type Movie struct {
	Id             string            `json:"Id"`
	Name           string            `json:"Name"`
	Overview       string            `json:"Overview"`
	ProductionYear int               `json:"ProductionYear"`
	ImageTags      map[string]string `json:"ImageTags"`
	Size           int64             `json:"Size"`
	SizeGB         float64           `json:"-"`
	Path           string            `json:"-"`
	DateAdded      time.Time         `json:"-"`
	DateCreated    string            `json:"DateCreated"`
	IsPlayed       bool              `json:"IsPlayed"`
}

// MediaSource represents a media file source
type MediaSource struct {
	Size int64  `json:"Size"`
	Path string `json:"Path"`
}

// MovieDetails contains detailed information about a movie
type MovieDetails struct {
	MediaSources []MediaSource `json:"MediaSources"`
	DateCreated  string        `json:"DateCreated"`
}

// MovieList is a list of movies
type MovieList struct {
	Items []Movie `json:"Items"`
}

// Episode represents a TV episode from Jellyfin
type Episode struct {
	Id                string `json:"Id"`
	Name              string `json:"Name"`
	SeriesId          string `json:"SeriesId"`
	SeriesName        string `json:"SeriesName"`
	SeasonId          string `json:"SeasonId"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
	IndexNumber       int    `json:"IndexNumber"`
	DateCreated       string `json:"DateCreated"`
	IsPlayed          bool   `json:"IsPlayed"`
}

// SeasonDetails contains details about a TV season
type SeasonDetails struct {
	Id          string `json:"Id"`
	Name        string `json:"Name"`
	ChildCount  int    `json:"ChildCount"`
	DateCreated string `json:"DateCreated"`
}

// EpisodeList is a list of episodes
type EpisodeList struct {
	Items []Episode `json:"Items"`
}

// SeasonList is a list of seasons
type SeasonList struct {
	Items []SeasonDetails `json:"Items"`
}

// SeasonInfo contains aggregated information about a season
type SeasonInfo struct {
	SeasonNumber  int
	SeasonId      string
	WatchedCount  int
	TotalCount    int
	SizeGB        float64
	DateAdded     time.Time
	IsFullyPlayed bool
}

// Series represents a TV series with seasons
type Series struct {
	Name      string
	Id        string
	ImageTags map[string]string `json:"ImageTags"`
	Seasons   []SeasonInfo
	TotalSize float64
}

// RefreshProgress tracks the progress of data refresh operations
type RefreshProgress struct {
	Current int
	Total   int
	Message string
}

// DeleteProgress tracks the progress of deletion operations
type DeleteProgress struct {
	Current     int
	Total       int
	Message     string
	CurrentItem string
	Percent     int
	// Stage tracking
	StageJellyfin     string // "pending", "processing", "complete", "error"
	StageInode        string // "pending", "processing", "complete", "error"
	StageRadarrSonarr string // "pending", "processing", "complete", "error"
	// Episode progress for seasons
	EpisodeCurrent int
	EpisodeTotal   int
	// Errors per stage
	StageErrors []string
}

// DeleteResult contains the results of a deletion operation
type DeleteResult struct {
	DeletedCount     int
	DeletedHardlinks int
	Errors           []string
	TotalSizeGB      float64
	DryRun           bool
	Items            []DeletedItem
}

// DeletedItem represents a single item that was deleted
type DeletedItem struct {
	Name             string
	Type             string // "movie" or "season"
	SizeGB           float64
	FilesDeleted     int
	HardlinksDeleted []string
	StageResults     StageResults
	Errors           []string
}

// StageResults contains the results of each deletion stage
type StageResults struct {
	Inode        StageResult
	RadarrSonarr StageResult
	Jellyfin     StageResult
}

// StageResult represents the result of a single deletion stage
type StageResult struct {
	Status  string // "success", "error", "skipped"
	Message string
	Details []string
}

// RadarrMovie represents a movie in Radarr
type RadarrMovie struct {
	Id        int    `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Path      string `json:"path"`
	Monitored bool   `json:"monitored"`
}

// RadarrMovieFile represents a movie file in Radarr
type RadarrMovieFile struct {
	Id      int    `json:"id"`
	MovieId int    `json:"movieId"`
	Path    string `json:"path"`
	Quality struct {
		Quality struct {
			Name string `json:"name"`
		} `json:"quality"`
	} `json:"quality"`
}

// SonarrSeason represents a season in Sonarr
type SonarrSeason struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// SonarrSeries represents a TV series in Sonarr
type SonarrSeries struct {
	Id      int            `json:"id"`
	Title   string         `json:"title"`
	Path    string         `json:"path"`
	Seasons []SonarrSeason `json:"seasons"`
}

// SonarrEpisodeFile represents an episode file in Sonarr
type SonarrEpisodeFile struct {
	Id           int    `json:"id"`
	SeriesId     int    `json:"seriesId"`
	SeasonNumber int    `json:"seasonNumber"`
	Path         string `json:"path"`
}

// StorageItem represents a single movie or series for the storage dashboard
type StorageItem struct {
	Name    string          `json:"name"`
	SizeGB  float64         `json:"sizeGB"`
	Watched bool            `json:"watched"`
	Seasons []StorageSeason `json:"seasons,omitempty"` // Only for TV series
}

// StorageSeason represents a season within a series
type StorageSeason struct {
	Number int     `json:"number"`
	SizeGB float64 `json:"sizeGB"`
}

// SizeDistributionBucket represents a size range bucket
type SizeDistributionBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// SizeDistribution shows how many items fall into each size bucket
type SizeDistribution struct {
	Movies map[string]int `json:"movies"`
	TV     map[string]int `json:"tv"`
}

// FilesystemStats represents storage filesystem information
type FilesystemStats struct {
	TotalGB float64 `json:"totalGB"`
	UsedGB  float64 `json:"usedGB"`
	FreeGB  float64 `json:"freeGB"`
	UsedPct float64 `json:"usedPct"`
}

// StorageDiskEntry is an on-disk path with measured size (from directory scan).
type StorageDiskEntry struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	SizeGB   float64 `json:"sizeGB"`
	Category string  `json:"category"`
}

// StorageLibraryOnDisk compares a library folder size to Jellyfin totals.
type StorageLibraryOnDisk struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Category   string  `json:"category"`
	DiskGB     float64 `json:"diskGB"`
	JellyfinGB float64 `json:"jellyfinGB"`
	DeltaGB    float64 `json:"deltaGB"`
}

// StorageOtherBreakdown explains disk usage outside Jellyfin movie/TV totals.
type StorageOtherBreakdown struct {
	TotalGB       float64                `json:"totalGB"`
	BeyondMediaGB float64                `json:"beyondMediaGB"`
	GapGB         float64                `json:"gapGB"`
	Items         []StorageDiskEntry     `json:"items"`
	Libraries     []StorageLibraryOnDisk `json:"libraries"`
}

// ScanStageStatus is a single stage in the shared library/storage scan.
type ScanStageStatus struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// ScanStatus tracks the one shared scan used by refresh, storage, and orphan views.
type ScanStatus struct {
	Scanning   bool              `json:"scanning"`
	Reason     string            `json:"reason"`
	Message    string            `json:"message"`
	Error      string            `json:"error,omitempty"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt"`
	ScannedAt  time.Time         `json:"scannedAt"`
	Stages     []ScanStageStatus `json:"stages"`
}

// StorageDataResponse represents the complete storage data for the dashboard API
type StorageDataResponse struct {
	Filesystem       FilesystemStats       `json:"filesystem"`
	Movies           StorageCategoryData   `json:"movies"`
	TV               StorageCategoryData   `json:"tv"`
	SizeDistribution SizeDistribution      `json:"sizeDistribution"`
	Other            StorageOtherBreakdown `json:"other"`
	Scan             ScanStatus            `json:"scan"`
	Timestamp        time.Time             `json:"timestamp"`
}

// OrphanFileEntry is a file with no hardlink across torrents and library paths.
type OrphanFileEntry struct {
	Path      string  `json:"path"`
	SizeGB    float64 `json:"sizeGB"`
	NLink     uint64  `json:"nlink"`
	Inode     uint64  `json:"inode"`
	Label     string  `json:"label,omitempty"`
	MediaType string  `json:"mediaType,omitempty"`
}

// OrphanScanResponse is the orphan hardlink scan API payload.
type OrphanScanResponse struct {
	Scanning         bool              `json:"scanning"`
	Message          string            `json:"message"`
	ScannedAt        time.Time         `json:"scannedAt"`
	TorrentsPath     string            `json:"torrentsPath"`
	LibraryPaths     []string          `json:"libraryPaths"`
	TorrentOrphans   []OrphanFileEntry `json:"torrentOrphans"`
	LibraryOrphans   []OrphanFileEntry `json:"libraryOrphans"`
	TorrentOrphansGB float64           `json:"torrentOrphansGB"`
	LibraryOrphansGB float64           `json:"libraryOrphansGB"`
}

// StorageCategoryData represents aggregated data for movies or TV
type StorageCategoryData struct {
	TotalGB   float64       `json:"totalGB"`
	Count     int           `json:"count"`
	Items     []StorageItem `json:"items"`
	Watched   int           `json:"watched"`
	Unwatched int           `json:"unwatched"`
}
