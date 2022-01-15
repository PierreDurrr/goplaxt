package internal

// Ids represent the IDs representing a media item accross the metadata providers
type Ids struct {
	Trakt  int    `json:"trakt"`
	Tvdb   int    `json:"tvdb"`
	Imdb   string `json:"imdb"`
	Tmdb   int    `json:"tmdb"`
	Tvrage int    `json:"tvrage"`
}

// Show represent a show's IDs
type Show struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	Ids   Ids
}

// ShowInfo represent a show
type ShowInfo struct {
	Show    Show
	Episode Episode
}

// Episode represent an episode
type Episode struct {
	Season int    `json:"season"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Ids    Ids    `json:"ids"`
}

// Season represent a season
type Season struct {
	Number   int
	Episodes []Episode
}

// Movie represent a movie
type Movie struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	Ids   Ids    `json:"ids"`
}

// MovieSearchResult represent a search result for a movie
type MovieSearchResult struct {
	Movie Movie
}

// ScrobbleBody represent the scrobbling status for a show or a movie
type ScrobbleBody struct {
	Progress int      `json:"progress"`
	Episode  *Episode `json:"episode,omitempty"`
	Movie    *Movie   `json:"movie,omitempty"`
}
