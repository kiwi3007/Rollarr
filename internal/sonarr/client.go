package sonarr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a typed HTTP client for the Sonarr v3 REST API.
type Client struct {
	BaseURL string
	APIKey  string
	http    *http.Client
}

// SeriesImage is one entry in Sonarr's images array for a series.
type SeriesImage struct {
	CoverType string `json:"coverType"` // "poster", "fanart", "banner"
	RemoteURL string `json:"remoteUrl"`
}

// Series represents a Sonarr series resource.
type Series struct {
	ID     int           `json:"id"`
	Title  string        `json:"title"`
	TvdbId int           `json:"tvdbId"`
	Images []SeriesImage `json:"images"`
}

func (s *Series) PosterURL() string {
	for _, img := range s.Images {
		if img.CoverType == "poster" && img.RemoteURL != "" {
			return img.RemoteURL
		}
	}
	return ""
}

func (s *Series) FanartURL() string {
	for _, img := range s.Images {
		if img.CoverType == "fanart" && img.RemoteURL != "" {
			return img.RemoteURL
		}
	}
	return ""
}

// Episode represents a Sonarr episode resource.
type Episode struct {
	ID            int  `json:"id"`
	SeriesId      int  `json:"seriesId"`
	SeasonNumber  int  `json:"seasonNumber"`
	EpisodeNumber int  `json:"episodeNumber"`
	HasFile       bool `json:"hasFile"`
	EpisodeFileId int  `json:"episodeFileId"`
	Monitored     bool `json:"monitored"`
}

// EpisodeFile represents a Sonarr episode file resource.
type EpisodeFile struct {
	ID            int `json:"id"`
	SeriesId      int `json:"seriesId"`
	SeasonNumber  int `json:"seasonNumber"`
	EpisodeNumber int `json:"episodeNumber"`
}

// NewClient constructs a Sonarr API client with a 30-second timeout.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// get performs an authenticated GET request and decodes the JSON response into out.
func (c *Client) get(path string, out interface{}) error {
	start := time.Now()
	log.Printf("[sonarr] GET %s", path)

	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("sonarr GET %s: build request: %w", path, err)
	}
	req.Header.Set("X-Api-Key", c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[sonarr] GET %s → error (%s): %v", path, time.Since(start), err)
		return fmt.Errorf("sonarr GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	log.Printf("[sonarr] GET %s → %d (%s)", path, resp.StatusCode, time.Since(start))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sonarr GET %s: status %d: %s", path, resp.StatusCode, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("sonarr GET %s: decode: %w", path, err)
	}
	return nil
}

// post performs an authenticated POST request with a JSON body.
func (c *Client) post(path string, body interface{}) error {
	return c.doWithBody(http.MethodPost, path, body)
}

// put performs an authenticated PUT request with a JSON body.
func (c *Client) put(path string, body interface{}) error {
	return c.doWithBody(http.MethodPut, path, body)
}

// doWithBody is the shared implementation for POST and PUT with JSON bodies.
func (c *Client) doWithBody(method, path string, body interface{}) error {
	start := time.Now()
	log.Printf("[sonarr] %s %s", method, path)

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("sonarr %s %s: marshal body: %w", method, path, err)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("sonarr %s %s: build request: %w", method, path, err)
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[sonarr] %s %s → error (%s): %v", method, path, time.Since(start), err)
		return fmt.Errorf("sonarr %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	log.Printf("[sonarr] %s %s → %d (%s)", method, path, resp.StatusCode, time.Since(start))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sonarr %s %s: status %d: %s", method, path, resp.StatusCode, b)
	}
	return nil
}

// delete performs an authenticated DELETE request.
func (c *Client) delete(path string) error {
	start := time.Now()
	log.Printf("[sonarr] DELETE %s", path)

	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("sonarr DELETE %s: build request: %w", path, err)
	}
	req.Header.Set("X-Api-Key", c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[sonarr] DELETE %s → error (%s): %v", path, time.Since(start), err)
		return fmt.Errorf("sonarr DELETE %s: %w", path, err)
	}
	defer resp.Body.Close()

	log.Printf("[sonarr] DELETE %s → %d (%s)", path, resp.StatusCode, time.Since(start))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sonarr DELETE %s: status %d: %s", path, resp.StatusCode, b)
	}
	return nil
}

// GetSeries returns all series known to Sonarr.
func (c *Client) GetSeries() ([]Series, error) {
	var result []Series
	if err := c.get("/api/v3/series", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetSeriesByTVDB returns the Sonarr series with the given TVDB ID, or an error
// if no match is found.
func (c *Client) GetSeriesByTVDB(tvdbId int) (*Series, error) {
	path := fmt.Sprintf("/api/v3/series?tvdbId=%d", tvdbId)
	var result []Series
	if err := c.get(path, &result); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("sonarr: no series with tvdbId=%d", tvdbId)
	}
	return &result[0], nil
}

// GetEpisodes returns all episodes for the given Sonarr series ID.
func (c *Client) GetEpisodes(seriesId int) ([]Episode, error) {
	path := fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesId)
	var result []Episode
	if err := c.get(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// MonitorEpisodes sets the monitored flag for the given episode IDs.
func (c *Client) MonitorEpisodes(episodeIds []int, monitored bool) error {
	body := map[string]interface{}{
		"episodeIds": episodeIds,
		"monitored":  monitored,
	}
	return c.put("/api/v3/episode/monitor", body)
}

// SearchEpisodes triggers a Sonarr episode search command for the given episode IDs.
func (c *Client) SearchEpisodes(episodeIds []int) error {
	body := map[string]interface{}{
		"name":       "EpisodeSearch",
		"episodeIds": episodeIds,
	}
	return c.post("/api/v3/command", body)
}

// DeleteEpisodeFile deletes the episode file with the given ID from Sonarr.
func (c *Client) DeleteEpisodeFile(fileId int) error {
	path := fmt.Sprintf("/api/v3/episodefile/%d", fileId)
	return c.delete(path)
}

// GetQueue returns the current Sonarr download queue.
func (c *Client) GetQueue() ([]map[string]interface{}, error) {
	// Sonarr wraps the queue in a paged response.
	var wrapper struct {
		Records []map[string]interface{} `json:"records"`
	}
	queueURL := "/api/v3/queue?" + url.Values{
		"pageSize": {"1000"},
	}.Encode()
	if err := c.get(queueURL, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Records, nil
}

// GetQueuedEpisodeIDs returns the set of episode IDs currently in the Sonarr
// download queue (grabbed, downloading, or importing). Episodes in this set
// should not be re-searched.
func (c *Client) GetQueuedEpisodeIDs() (map[int]bool, error) {
	var wrapper struct {
		Records []struct {
			EpisodeID int `json:"episodeId"`
		} `json:"records"`
	}
	queueURL := "/api/v3/queue?" + url.Values{"pageSize": {"1000"}}.Encode()
	if err := c.get(queueURL, &wrapper); err != nil {
		return nil, err
	}
	ids := make(map[int]bool, len(wrapper.Records))
	for _, r := range wrapper.Records {
		if r.EpisodeID > 0 {
			ids[r.EpisodeID] = true
		}
	}
	return ids, nil
}
