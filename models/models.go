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
	SeasonNumber int
	SeasonId     string
	WatchedCount int
	TotalCount   int
	SizeGB       float64
	DateAdded    time.Time
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
	Name              string
	Type              string // "movie" or "season"
	SizeGB            float64
	FilesDeleted      int
	HardlinksDeleted  []string
	StageResults      StageResults
	Errors            []string
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
