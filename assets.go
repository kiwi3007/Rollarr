// Package rollarr provides the embedded frontend assets. This file lives at
// the repository root (alongside frontend/) so that the embed directive can
// reference frontend/dist as a relative path.
package rollarr

import "embed"

// FrontendFS holds the compiled React SPA embedded at build time.
//
//go:embed all:frontend/dist
var FrontendFS embed.FS
