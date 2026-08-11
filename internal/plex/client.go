package plex

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a typed HTTP client for the Plex Media Server HTTP API.
type Client struct {
	BaseURL string
	Token   string
	http    *http.Client
}

// WatchHistoryEntry is a single entry from the Plex watch history.
type WatchHistoryEntry struct {
	AccountID  int
	EpisodeKey string    // e.g. /library/metadata/12345
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

// addToken appends the Plex authentication token as a query parameter.
func (c *Client) addToken(rawURL string) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "X-Plex-Token=" + url.QueryEscape(c.Token)
}

// getJSON performs a GET request and JSON-decodes the response body into out.
func (c *Client) getJSON(path string, out interface{}) error {
	start := time.Now()
	log.Printf("[plex] GET %s (json)", path)

	fullURL := c.BaseURL + path
	req, err := http.NewRequest(http.MethodGet, c.addToken(fullURL), nil)
	if err != nil {
		return fmt.Errorf("plex GET %s: build request: %w", path, err)
	}
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
	req, err := http.NewRequest(http.MethodGet, c.addToken(fullURL), nil)
	if err != nil {
		return fmt.Errorf("plex GET %s: build request: %w", path, err)
	}
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
	Key           string `xml:"key,attr"`
	GrandparentKey string `xml:"grandparentKey,attr"`
	ParentIndex   string `xml:"parentIndex,attr"`   // season number
	Index         string `xml:"index,attr"`         // episode number
	AccountID     string `xml:"accountID,attr"`
	ViewedAt      string `xml:"viewedAt,attr"` // unix timestamp
}

// ---- Plex users XML types --------------------------------------------------

// mediaContainerUsers parses plex.tv /api/users responses (<User> elements).
type mediaContainerUsers struct {
	XMLName xml.Name   `xml:"MediaContainer"`
	Users   []plexUser `xml:"User"`
}

// mediaContainerAccounts parses local /accounts responses (<Account> elements).
type mediaContainerAccounts struct {
	XMLName  xml.Name     `xml:"MediaContainer"`
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

// FindShowByTVDB searches the Plex library for a show matching the given TVDB
// ID and returns its ratingKey, or an error if not found.
// Falls through to a full section scan that checks both legacy and modern GUIDs.
func (c *Client) FindShowByTVDB(tvdbId int) (string, error) {
	return c.findShowByTVDBFallback(tvdbId)
}

// findShowByTVDBFallback scans all TV library sections using the JSON API so
// that both legacy (com.plexapp.agents.thetvdb) and modern (tvdb://) GUIDs
// are checked. Modern Plex agents use plex://show/ as the primary guid and
// store tvdb://XXXXX in the Guid array — only the JSON response includes that
// array (XML only has the primary guid attribute).
func (c *Client) findShowByTVDBFallback(tvdbId int) (string, error) {
	// Use XML for sections list (known to work); JSON for individual section
	// content so we get the Guid array needed to detect modern Plex agent shows.
	var sections struct {
		XMLName xml.Name `xml:"MediaContainer"`
		Dirs    []struct {
			Key  string `xml:"key,attr"`
			Type string `xml:"type,attr"`
		} `xml:"Directory"`
	}
	if err := c.getXML("/library/sections", &sections); err != nil {
		return "", fmt.Errorf("plex.FindShowByTVDB: get sections: %w", err)
	}

	modernGUID := fmt.Sprintf("tvdb://%d", tvdbId)
	legacyGUID := fmt.Sprintf("com.plexapp.agents.thetvdb://%d", tvdbId)

	type jsonGuid struct {
		ID string `json:"id"`
	}
	type jsonShow struct {
		RatingKey string     `json:"ratingKey"`
		Title     string     `json:"title"`
		GUID      string     `json:"guid"`
		Guid      []jsonGuid `json:"Guid"`
	}
	var showsResp struct {
		MediaContainer struct {
			Metadata []jsonShow `json:"Metadata"`
		} `json:"MediaContainer"`
	}

	for _, sec := range sections.Dirs {
		if sec.Type != "show" {
			continue
		}
		if err := c.getJSON("/library/sections/"+sec.Key+"/all?type=2&includeGuids=1", &showsResp); err != nil {
			continue
		}
		for _, show := range showsResp.MediaContainer.Metadata {
			if strings.Contains(show.GUID, legacyGUID) {
				log.Printf("[plex] FindShowByTVDB tvdb=%d → %q (legacy GUID)", tvdbId, show.Title)
				return show.RatingKey, nil
			}
			for _, g := range show.Guid {
				if g.ID == modernGUID {
					log.Printf("[plex] FindShowByTVDB tvdb=%d → %q (modern GUID)", tvdbId, show.Title)
					return show.RatingKey, nil
				}
			}
		}
	}

	return "", fmt.Errorf("plex: show with tvdbId=%d not found in library", tvdbId)
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
	for _, g := range md.Guid {
		if rest, ok := strings.CutPrefix(g.ID, "tvdb://"); ok {
			if id, err := strconv.Atoi(rest); err == nil && id > 0 {
				return id, nil
			}
		}
	}
	// Legacy agent: com.plexapp.agents.thetvdb://121361?lang=en
	if rest, ok := strings.CutPrefix(md.GUID, "com.plexapp.agents.thetvdb://"); ok {
		rest, _, _ = strings.Cut(rest, "?")
		if id, err := strconv.Atoi(rest); err == nil && id > 0 {
			return id, nil
		}
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

// GetUsers returns all Plex managed/home users visible to the local server.
func (c *Client) GetUsers() ([]PlexUser, error) {
	var mc mediaContainerAccounts
	if err := c.getXML("/accounts", &mc); err != nil {
		return nil, fmt.Errorf("plex.GetUsers: %w", err)
	}

	users := make([]PlexUser, 0, len(mc.Accounts))
	for _, u := range mc.Accounts {
		id, _ := strconv.Atoi(u.ID)
		if id == 0 {
			continue
		}
		users = append(users, PlexUser{
			ID:       id,
			Username: u.Name,
		})
	}
	return users, nil
}

// GetPlexTVUsers fetches friend/shared-library users from the plex.tv cloud
// API. These accounts are not returned by the local /accounts endpoint — their
// IDs are plex.tv account IDs (large integers) and are used as accountID in
// watch history entries for non-home users.
func (c *Client) GetPlexTVUsers() ([]PlexUser, error) {
	var mc mediaContainerUsers
	// plex.tv cloud endpoint — absolute URL, not relative to BaseURL.
	fullURL := "https://plex.tv/api/users?X-Plex-Token=" + url.QueryEscape(c.Token)
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("plex.GetPlexTVUsers: build request: %w", err)
	}
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
func (c *Client) BuildUserMap() (map[string]int, error) {
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

	return m, nil
}
