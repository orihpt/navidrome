package server

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/log"
)

func NewRecommendationsProxy(rawURL string) http.Handler {
	target, err := url.Parse(rawURL)
	if err != nil {
		log.Error("Invalid Waves Music recommendation URL", "url", rawURL, err)
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Recommendation service is not configured", http.StatusServiceUnavailable)
		})
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	mountPath := path.Join(conf.Server.BasePath, consts.URLPathNativeAPI, "v1/recommend")
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.URL.Path = joinProxyPath(target.Path, stripRecommendationMount(req.URL.Path, mountPath))
		req.Header.Set("X-Forwarded-Host", req.Host)
		req.Header.Set("X-Waves-Music-Backend", "navidrome")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Warn(r.Context(), "Recommendation proxy request failed", "url", rawURL, err)
		http.Error(w, "Recommendation service unavailable", http.StatusBadGateway)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Head(rawURL)
			if err != nil {
				http.Error(w, "Recommendation service unavailable", http.StatusServiceUnavailable)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 500 {
				http.Error(w, "Recommendation service unavailable", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func stripRecommendationMount(requestPath, mountPath string) string {
	for _, prefix := range []string{mountPath, "/api/v1/recommend"} {
		if after, ok := strings.CutPrefix(requestPath, prefix); ok {
			if after == "" {
				return "/"
			}
			return after
		}
	}
	return requestPath
}

func joinProxyPath(basePath, requestPath string) string {
	if basePath == "" || basePath == "/" {
		if strings.HasPrefix(requestPath, "/") {
			return requestPath
		}
		return "/" + requestPath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(requestPath, "/")
}
