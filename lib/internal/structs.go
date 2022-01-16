package internal

// Ids represent the IDs representing a media item accross the metadata providers
type Ids struct {
	Trakt *int    `json:"trakt,omitempty"`
	Tvdb  *int    `json:"tvdb,omitempty"`
	Imdb  *string `json:"imdb,omitempty"`
	Tmdb  *int    `json:"tmdb,omitempty"`
	Slug  *string `json:"slug,omitempty"`
}

// Show represent a show's IDs
type Show struct {
	Title *string `json:"title,omitempty"`
	Year  *int    `json:"year,omitempty"`
	Ids   Ids
}

// ShowInfo represent a show
type ShowInfo struct {
	Show    Show
	Episode Episode
}

// Episode represent an episode
type Episode struct {
	Season int     `json:"season"`
	Number int     `json:"number"`
	Title  *string `json:"title,omitempty"`
	Ids    *Ids    `json:"ids,omitempty"`
}

// Season represent a season
type Season struct {
	Number   int
	Episodes []Episode
}

// Movie represent a movie
type Movie struct {
	Title *string `json:"title,omitempty"`
	Year  *int    `json:"year,omitempty"`
	Ids   Ids     `json:"ids"`
}

// MovieSearchResult represent a search result for a movie
type MovieSearchResult struct {
	Movie Movie
}

// ScrobbleBody represent the scrobbling status for a show or a movie
type ScrobbleBody struct {
	Progress int      `json:"progress"`
	Movie    *Movie   `json:"movie,omitempty"`
	Show     *Show    `json:"show,omitempty"`
	Episode  *Episode `json:"episode,omitempty"`
}

// CacheItem represent an item in cache
type CacheItem struct {
	Body        ScrobbleBody `json:"body"`
	LastAction  string       `json:"last_action"`
	AccessToken string       `json:"access_token"`
}
