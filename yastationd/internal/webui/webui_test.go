package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerWithoutBuiltAppServesFallback(t *testing.T) {
	root, err := fs.Sub(dist, "dist/app")
	if err == nil {
		if _, err := fs.Stat(root, "index.html"); err == nil {
			t.Skip("test exercises the deliberately app-less plain-Go build")
		}
	}
	res := httptest.NewRecorder()
	Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/settings/network", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := res.Body.String(); got != notBuilt || !strings.Contains(got, "build it with") {
		t.Fatalf("fallback body = %q", got)
	}
}

func TestHandlerServesBuiltAppAndCachesAssets(t *testing.T) {
	root, err := fs.Sub(dist, "dist/app")
	if err != nil {
		t.Skip("web app is not embedded")
	}
	if _, err := fs.Stat(root, "index.html"); err != nil {
		t.Skip("web app is not embedded")
	}

	route := httptest.NewRecorder()
	Handler().ServeHTTP(route, httptest.NewRequest(http.MethodGet, "/settings/network", nil))
	if route.Code != http.StatusOK {
		t.Fatalf("SPA route status = %d", route.Code)
	}
	if got := route.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("SPA route cache = %q", got)
	}
	body := route.Body.String()
	if !strings.Contains(body, "<!doctype html>") {
		t.Fatalf("SPA route did not serve index.html: %q", body[:min(80, len(body))])
	}

	entries, err := fs.ReadDir(root, "assets")
	if err != nil {
		t.Fatal(err)
	}
	var assetName string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			assetName = entry.Name()
			break
		}
	}
	if assetName == "" {
		t.Fatal("embedded app has no JavaScript asset")
	}
	asset := httptest.NewRecorder()
	Handler().ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/"+assetName, nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("asset status = %d", asset.Code)
	}
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache = %q", got)
	}
}
