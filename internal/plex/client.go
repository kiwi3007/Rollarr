package plex

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is a typed HTTP client for the Plex Media Server HTTP API.
type Client struct {
	BaseURL string
	Token   string
	http    *http.Client

	libMu    sync.Mutex
	libIndex map[int]string // tvdbId → ratingKey
	libBuilt time.Time

	usersMu    sync.Mutex
	users      map[string]int
	usersBuilt time.Time
}

// WatchHistoryEntry is a single entry from the Plex watch history.
type WatchHistoryEntry struct {
	AccountID  int
	EpisodeKey string // e.g. /library/metadata/12345
	ViewedAt   time.Time
	SeasonNum  int
	EpisodeNum int
}

// PlexUser represents a Plex account (managed or home user).
type PlexUser struct {
	ID       int
	Username string
	Email    string
}

// NewClient constructs a Plex HTTP client with a 30-second timeout.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// authorize sets the Plex token as a header. It must never go in the URL:
// Go's transport errors embed the full request URL, and those errors are
// logged and served by /api/logs.
func (c *Client) authorize(req *http.Request) {
	req.Header.Set("X-Plex-Token", c.Token)
}

// getJSON performs a GET request and JSON-decodes the response body into out.
func (c *Client) getJSON(path string, out interface{}) error {
	start := time.Now()
	log.Printf("[plex] GET %s (json)", path)

	fullURL := c.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("plex GET %s: build request: %w", path, err)
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[plex] GET %s → error (%s): %v", path, time.Since(start), err)
		return fmt.Errorf("plex GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	log.Printf("[plex] GET %s → %d (%s)", path, resp.StatusCode, time.Since(start))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("plex GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// getXML performs a GET request and XML-decodes the response body into out.
func (c *Client) getXML(path string, out interface{}) error {
	start := time.Now()
	log.Printf("[plex] GET %s", path)

	fullURL := c.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("plex GET %s: build request: %w", path, err)
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("[plex] GET %s → error (%s): %v", path, time.Since(start), err)
		return fmt.Errorf("plex GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	log.Printf("[plex] GET %s → %d (%s)", path, resp.StatusCode, time.Since(start))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("plex GET %s: status %d: %s", path, resp.StatusCode, body)
	}

	if err := xml.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("plex GET %s: decode XML: %w", path, err)
	}
	return nil
}

// ---- XML response types ----------------------------------------------------

type mediaContainer struct {
	XMLName xml.Name `xml:"MediaContainer"`
	Videos  []video  `xml:"Video"`
}

type video struct {
	Key            string `xml:"key,attr"`
	GrandparentKey string `xml:"grandparentKey,attr"`
	ParentIndex    string `xml:"parentIndex,attr"` // season number
	Index          string `xml:"index,attr"`       // episode number
	AccountID      string `xml:"accountID,attr"`
	ViewedAt       string `xml:"viewedAt,attr"` // unix timestamp
}

// ---- Plex users XML types --------------------------------------------------

// mediaContainerUsers parses plex.tv /api/users responses (<User> elements).
type mediaContainerUsers struct {
	XMLName xml.Name   `xml:"MediaContainer"`
	Users   []plexUser `xml:"User"`
}

// mediaContainerAccounts parses local /accounts responses (<Account> elements).
type mediaContainerAccounts struct {
	XMLName  xml.Name      `xml:"MediaContainer"`
	Accounts []plexAccount `xml:"Account"`
}

type plexUser struct {
	ID    string `xml:"id,attr"`
	Name  string `xml:"name,attr"`  // Plex username (login name)
	Title string `xml:"title,attr"` // display name
	Email string `xml:"email,attr"`
}

type plexAccount struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"` // local account name (login / display)
}

// ---- Library search XML types ----------------------------------------------

type mediaContainerDirectory struct {
	XMLName     xml.Name    `xml:"MediaContainer"`
	Directories []directory `xml:"Directory"`
}

type directory struct {
	RatingKey string `xml:"ratingKey,attr"`
	GUID      string `xml:"guid,attr"`
	Title     string `xml:"title,attr"`
}

// historyContainerSize is sent explicitly on history requests so PMS never
// silently pages the result — a truncated history would under-report a user's
// highest-watched episode.
const historyContainerSize = 10000

// GetShowHistory returns all watch history entries for the show identified by
// the given Plex ratingKey (e.g. "12345").
func (c *Client) GetShowHistory(showKey string) ([]WatchHistoryEntry, error) {
	// Strip leading slash if present in the key.
	key := strings.TrimPrefix(showKey, "/library/metadata/")
	path := fmt.Sprintf("/status/sessions/history/all?type=4&metadataItemID=%s&X-Plex-Container-Start=0&X-Plex-Container-Size=%d",
		url.QueryEscape(key), historyContainerSize)

	var mc mediaContainer
	if err := c.getXML(path, &mc); err != nil {
		return nil, fmt.Errorf("plex.GetShowHistory(%s): %w", showKey, err)
	}

	return parseHistory(mc.Videos)
}

// GetAccountHistory returns watch history for a specific Plex account filtered
// to the show identified by showKey.
func (c *Client) GetAccountHistory(showKey string, accountId int) ([]WatchHistoryEntry, error) {
	key := strings.TrimPrefix(showKey, "/library/metadata/")
	path := fmt.Sprintf(
		"/status/sessions/history/all?type=4&accountID=%d&metadataItemID=%s&X-Plex-Container-Start=0&X-Plex-Container-Size=%d",
		accountId, url.QueryEscape(key), historyContainerSize,
	)

	var mc mediaContainer
	if err := c.getXML(path, &mc); err != nil {
		return nil, fmt.Errorf("plex.GetAccountHistory(%s, %d): %w", showKey, accountId, err)
	}

	return parseHistory(mc.Videos)
}

func parseHistory(videos []video) ([]WatchHistoryEntry, error) {
	entries := make([]WatchHistoryEntry, 0, len(videos))
	for _, v := range videos {
		accountID, _ := strconv.Atoi(v.AccountID)
		season, _ := strconv.Atoi(v.ParentIndex)
		episode, _ := strconv.Atoi(v.Index)
		ts, _ := strconv.ParseInt(v.ViewedAt, 10, 64)
		entries = append(entries, WatchHistoryEntry{
			AccountID:  accountID,
			EpisodeKey: v.Key,
			ViewedAt:   time.Unix(ts, 0),
			SeasonNum:  season,
			EpisodeNum: episode,
		})
	}
	return entries, nil
}

// Library index caching. FindShowByTVDB used to download every show in every
// TV section on each call, several times per show per reconcile. The index
// maps tvdbId → ratingKey for the whole library and is rebuilt when stale, or
// on a miss (a show just added to Plex) at most once per libraryMissRescan.
const (
	libraryIndexTTL   = 30 * time.Minute
	libraryMissRescan = 2 * time.Minute
)

// tvdbFromGuids extracts the TVDB ID from a Plex item's Guid array (modern
// agent, "tvdb://121361") or primary guid (legacy agent,
// "com.plexapp.agents.thetvdb://121361?lang=en"). Returns 0 when absent.
func tvdbFromGuids(primary string, guids []string) int {
	for _, g := range guids {
		if rest, ok := strings.CutPrefix(g, "tvdb://"); ok {
			if id, err := strconv.Atoi(rest); err == nil && id > 0 {
				return id
			}
		}
	}
	if rest, ok := strings.CutPrefix(primary, "com.plexapp.agents.thetvdb://"); ok {
		rest, _, _ = strings.Cut(rest, "?")
		if id, err := strconv.Atoi(rest); err == nil && id > 0 {
			return id
		}
	}
	return 0
}

// ErrShowNotFound means Plex answered and the show is in no TV library —
// distinct from Plex being unreachable, where watch history is unknown.
var ErrShowNotFound = errors.New("show not found in Plex library")

// FindShowByTVDB returns the Plex ratingKey of the show with the given TVDB
// ID. It wraps ErrShowNotFound when the show is in no TV library; any other
// error means Plex couldn't be read.
func (c *Client) FindShowByTVDB(tvdbId int) (string, error) {
	c.libMu.Lock()
	defer c.libMu.Unlock()

	now := time.Now()
	if c.libIndex == nil || now.Sub(c.libBuilt) > libraryIndexTTL {
		if err := c.rebuildLibraryIndexLocked(now); err != nil {
			return "", err
		}
	}
	if key, ok := c.libIndex[tvdbId]; ok {
		return key, nil
	}
	if now.Sub(c.libBuilt) > libraryMissRescan {
		if err := c.rebuildLibraryIndexLocked(now); err != nil {
			return "", err
		}
		if key, ok := c.libIndex[tvdbId]; ok {
			return key, nil
		}
	}
	return "", fmt.Errorf("plex: tvdbId=%d: %w", tvdbId, ErrShowNotFound)
}

// rebuildLibraryIndexLocked refreshes the tvdbId → ratingKey index.
func (c *Client) rebuildLibraryIndexLocked(now time.Time) error {
	shows, err := c.ListLibraryShows()
	if err != nil {
		return fmt.Errorf("plex library index: %w", err)
	}
	index := make(map[int]string, len(shows))
	for _, show := range shows {
		if show.TVDBId > 0 {
			index[show.TVDBId] = show.RatingKey
		}
	}
	c.libIndex = index
	c.libBuilt = now
	log.Printf("[plex] library index rebuilt: %d shows", len(index))
	return nil
}

// LibraryShow is one TV show entry in the Plex library.
type LibraryShow struct {
	RatingKey string
	Title     string
	Year      int
	TVDBId    int // 0 if the show has no TVDB GUID
}

// ListLibraryShows returns every show across all TV library sections with its
// TVDB ID resolved from either the legacy or modern agent GUID. It uses the
// JSON API for section contents because only JSON includes the Guid array
// that modern Plex agents (primary guid plex://show/…) store tvdb:// in. A
// failed section fails the whole call: a partial list would make shows look
// absent from Plex.
func (c *Client) ListLibraryShows() ([]LibraryShow, error) {
	var sections struct {
		XMLName xml.Name `xml:"MediaContainer"`
		Dirs    []struct {
			Key  string `xml:"key,attr"`
			Type string `xml:"type,attr"`
		} `xml:"Directory"`
	}
	if err := c.getXML("/library/sections", &sections); err != nil {
		return nil, fmt.Errorf("plex.ListLibraryShows: get sections: %w", err)
	}

	var result []LibraryShow
	for _, sec := range sections.Dirs {
		if sec.Type != "show" {
			continue
		}
		// Declared per section: decoding into a reused struct keeps stale
		// Guid entries from the previous section's slice elements.
		var resp struct {
			MediaContainer struct {
				Metadata []struct {
					RatingKey string `json:"ratingKey"`
					Title     string `json:"title"`
					Year      int    `json:"year"`
					GUID      string `json:"guid"`
					Guid      []struct {
						ID string `json:"id"`
					} `json:"Guid"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := c.getJSON("/library/sections/"+url.PathEscape(sec.Key)+"/all?type=2&includeGuids=1", &resp); err != nil {
			return nil, fmt.Errorf("plex.ListLibraryShows: section %s: %w", sec.Key, err)
		}
		for _, show := range resp.MediaContainer.Metadata {
			guids := make([]string, 0, len(show.Guid))
			for _, g := range show.Guid {
				guids = append(guids, g.ID)
			}
			result = append(result, LibraryShow{
				RatingKey: show.RatingKey,
				Title:     show.Title,
				Year:      show.Year,
				TVDBId:    tvdbFromGuids(show.GUID, guids),
			})
		}
	}
	return result, nil
}

// GetShowTVDBID resolves a show's Plex ratingKey to its TVDB ID by reading the
// item's Guid array (modern agent) or primary guid (legacy agent). Used by the
// Plex webhook, whose payload carries only the ratingKey.
func (c *Client) GetShowTVDBID(ratingKey string) (int, error) {
	key := strings.TrimPrefix(ratingKey, "/library/metadata/")

	var resp struct {
		MediaContainer struct {
			Metadata []struct {
				GUID string `json:"guid"`
				Guid []struct {
					ID string `json:"id"`
				} `json:"Guid"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.getJSON("/library/metadata/"+url.PathEscape(key)+"?includeGuids=1", &resp); err != nil {
		return 0, fmt.Errorf("plex.GetShowTVDBID(%s): %w", ratingKey, err)
	}
	if len(resp.MediaContainer.Metadata) == 0 {
		return 0, fmt.Errorf("plex.GetShowTVDBID(%s): no metadata", ratingKey)
	}

	md := resp.MediaContainer.Metadata[0]
	guids := make([]string, 0, len(md.Guid))
	for _, g := range md.Guid {
		guids = append(guids, g.ID)
	}
	if id := tvdbFromGuids(md.GUID, guids); id > 0 {
		return id, nil
	}
	return 0, fmt.Errorf("plex.GetShowTVDBID(%s): no tvdb guid", ratingKey)
}

// GetMachineIdentifier returns the local server's machineIdentifier — the
// stable UUID Plex also reports as Server.uuid in webhook payloads. Used to
// tell scrobbles that happened on this server apart from scrobbles the account
// generated on somebody else's server.
func (c *Client) GetMachineIdentifier() (string, error) {
	var resp struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err := c.getJSON("/", &resp); err != nil {
		return "", fmt.Errorf("plex.GetMachineIdentifier: %w", err)
	}
	if resp.MediaContainer.MachineIdentifier == "" {
		return "", fmt.Errorf("plex.GetMachineIdentifier: empty identifier")
	}
	return resp.MediaContainer.MachineIdentifier, nil
}

// Ping checks that the server is reachable and the token is accepted. It hits
// an authenticated endpoint on purpose: /identity answers without a token, so
// it would report healthy with an expired one.
func (c *Client) Ping() error {
	var mc struct {
		XMLName xml.Name `xml:"MediaContainer"`
	}
	if err := c.getXML("/library/sections", &mc); err != nil {
		return fmt.Errorf("plex.Ping: %w", err)
	}
	return nil
}

// OwnerAccountID returns the plex.tv account ID of the token's owner (the
// server owner, in a normal setup).
func (c *Client) OwnerAccountID() (int, error) {
	req, err := http.NewRequest(http.MethodGet, "https://plex.tv/users/account", nil)
	if err != nil {
		return 0, fmt.Errorf("plex.OwnerAccountID: build request: %w", err)
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("plex.OwnerAccountID: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("plex.OwnerAccountID: status %d", resp.StatusCode)
	}
	var user struct {
		XMLName xml.Name `xml:"user"`
		ID      string   `xml:"id,attr"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&user); err != nil {
		return 0, fmt.Errorf("plex.OwnerAccountID: decode: %w", err)
	}
	id, err := strconv.Atoi(user.ID)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("plex.OwnerAccountID: bad id %q", user.ID)
	}
	return id, nil
}

// GetPlexTVUsers fetches friend/shared-library users from the plex.tv cloud
// API. These accounts are not returned by the local /accounts endpoint — their
// IDs are plex.tv account IDs (large integers) and are used as accountID in
// watch history entries for non-home users.
func (c *Client) GetPlexTVUsers() ([]PlexUser, error) {
	var mc mediaContainerUsers
	// plex.tv cloud endpoint — absolute URL, not relative to BaseURL.
	req, err := http.NewRequest(http.MethodGet, "https://plex.tv/api/users", nil)
	if err != nil {
		return nil, fmt.Errorf("plex.GetPlexTVUsers: build request: %w", err)
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex.GetPlexTVUsers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plex.GetPlexTVUsers: status %d: %s", resp.StatusCode, body)
	}
	if err := xml.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("plex.GetPlexTVUsers: decode XML: %w", err)
	}

	users := make([]PlexUser, 0, len(mc.Users))
	for _, u := range mc.Users {
		id, _ := strconv.Atoi(u.ID)
		if id == 0 {
			continue
		}
		users = append(users, PlexUser{
			ID:       id,
			Username: u.Title,
			Email:    u.Email,
		})
	}
	return users, nil
}

// BuildUserMap returns a map of username → accountID for all Plex users.
// It merges the local /accounts list (managed home users) with the plex.tv
// cloud user list (friend/shared-library users) so that both home and
// non-home accounts resolve correctly. Both display name and login name are
// indexed. A cloud lookup failure is non-fatal — local accounts are always
// returned.
//
// The result is cached for userMapTTL: the dashboard, the state engine and
// webhooks all call this, and each build is two HTTP requests, one of them to
// plex.tv. Callers that just failed to resolve a name use RefreshUserMap.
func (c *Client) BuildUserMap() (map[string]int, error) {
	c.usersMu.Lock()
	defer c.usersMu.Unlock()
	if c.users != nil && time.Since(c.usersBuilt) < userMapTTL {
		return copyUserMap(c.users), nil
	}
	return c.buildUserMapLocked()
}

// RefreshUserMap rebuilds the user map, bypassing the cache. Used when a
// lookup misses, e.g. a friend added to Plex since the last build.
func (c *Client) RefreshUserMap() (map[string]int, error) {
	c.usersMu.Lock()
	defer c.usersMu.Unlock()
	return c.buildUserMapLocked()
}

const userMapTTL = 10 * time.Minute

func copyUserMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (c *Client) buildUserMapLocked() (map[string]int, error) {
	var mc mediaContainerAccounts
	if err := c.getXML("/accounts", &mc); err != nil {
		return nil, fmt.Errorf("plex.BuildUserMap: %w", err)
	}

	m := make(map[string]int, len(mc.Accounts)*2)
	for _, u := range mc.Accounts {
		id, _ := strconv.Atoi(u.ID)
		if id == 0 {
			continue
		}
		if u.Name != "" {
			m[u.Name] = id
		}
	}

	// Merge plex.tv friend accounts. Failures are logged but non-fatal so that
	// a cloud outage doesn't break resolution of local home users.
	friends, err := c.GetPlexTVUsers()
	if err != nil {
		log.Printf("[plex] BuildUserMap: plex.tv friends lookup failed (non-fatal): %v", err)
	}
	for _, f := range friends {
		if f.Username != "" {
			m[f.Username] = f.ID
		}
		if f.Email != "" {
			m[f.Email] = f.ID
		}
	}

	// Only cache a complete map; a partial one (plex.tv down) is retried on
	// the next call.
	if err == nil {
		c.users = m
		c.usersBuilt = time.Now()
	}
	return copyUserMap(m), nil
}
