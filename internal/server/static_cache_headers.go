package server

import "net/http"

const (
	staticBrowserCacheControl    = "public, max-age=0, must-revalidate"
	staticCDNCacheControl        = "public, max-age=300"
	staticCloudflareCacheControl = "public, max-age=300"
)

func setStaticResourceCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", staticBrowserCacheControl)
	w.Header().Set("CDN-Cache-Control", staticCDNCacheControl)
	w.Header().Set("Cloudflare-CDN-Cache-Control", staticCloudflareCacheControl)
}
