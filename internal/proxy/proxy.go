package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

// Proxy wraps httputil.ReverseProxy and intercepts specific Sonarr endpoints
// to filter requests to the computed buffer window.
// The target Sonarr URL is resolved on each request via getTarget so that
// settings changes take effect without a restart.
type Proxy struct {
	getTarget func() string
	intercept *Interceptor

	mu     sync.RWMutex
	rp     *httputil.ReverseProxy
	rpURL  string
}

// NewProxy constructs a Proxy that forwards all traffic to the URL returned by
// getTarget, except for the monitored intercept endpoints.
func NewProxy(getTarget func() string, intercept *Interceptor) *Proxy {
	return &Proxy{getTarget: getTarget, intercept: intercept}
}

// getReverseProxy returns a cached reverse proxy for the current sonarr URL,
// rebuilding it only when the URL has changed.
func (p *Proxy) getReverseProxy() (*httputil.ReverseProxy, error) {
	targetURL := p.getTarget()
	if targetURL == "" {
		return nil, fmt.Errorf("sonarr_url not configured in Rollarr settings")
	}

	p.mu.RLock()
	if p.rpURL == targetURL && p.rp != nil {
		rp := p.rp
		p.mu.RUnlock()
		return rp, nil
	}
	p.mu.RUnlock()

	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid sonarr_url %q: %w", targetURL, err)
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = target.Host
	}

	p.mu.Lock()
	p.rp = rp
	p.rpURL = targetURL
	p.mu.Unlock()

	return rp, nil
}

// ServeHTTP routes the request: intercepted endpoints are filtered; all others
// are forwarded via the reverse proxy.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, err := p.getReverseProxy()
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusServiceUnavailable)
		return
	}

	switch {
	case r.Method == http.MethodPost && isPath(r.URL.Path, "/api/v3/series"):
		result, handled := p.intercept.InterceptSeriesAdd(w, r)
		if handled {
			return
		}
		rp.ServeHTTP(result, result.Request)
		if result.succeeded() && result.TvdbId != 0 && p.intercept.OnSeriesAdd != nil {
			tvdbId := result.TvdbId
			go p.intercept.OnSeriesAdd(tvdbId)
		}

	case r.Method == http.MethodPut && isPath(r.URL.Path, "/api/v3/episode/monitor"):
		rewritten, handled := p.intercept.InterceptMonitor(w, r)
		if handled {
			return
		}
		rp.ServeHTTP(w, rewritten)

	case r.Method == http.MethodPost && isPath(r.URL.Path, "/api/v3/command"):
		rewritten, handled := p.intercept.InterceptSearch(w, r)
		if handled {
			return
		}
		rp.ServeHTTP(w, rewritten)

	default:
		rp.ServeHTTP(w, r)
	}
}

// isPath reports whether the request path matches the target, ignoring a
// trailing slash on either side.
func isPath(requestPath, target string) bool {
	return strings.TrimRight(requestPath, "/") == strings.TrimRight(target, "/")
}
