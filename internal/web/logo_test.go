//go:build uitest

package web

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func TestLogoAsset(t *testing.T) {
	source, err := os.ReadFile("../../logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	bundled, err := assets.ReadFile("static/logo.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, bundled) {
		t.Fatal("bundled logo differs from README logo")
	}
}

func TestLogoThemeBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.FS(static)))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	for _, tc := range []struct{ mode, color string }{{"dark", "rgb(255, 255, 255)"}, {"light", "rgb(10, 10, 10)"}} {
		t.Run(tc.mode, func(t *testing.T) {
			var got struct {
				Fill, Stroke string
				Transparent  bool
			}
			err := chromedp.Run(tab,
				emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: tc.mode}}),
				chromedp.Navigate(server.URL+"/logo.svg"),
				chromedp.Evaluate(`({Fill:getComputedStyle(document.querySelector('path[fill]')).fill,Stroke:getComputedStyle(document.querySelector('path[stroke]')).stroke,Transparent:!document.querySelector('rect')&&document.documentElement.getAttribute('fill')==='none'})`, &got),
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Fill != tc.color || got.Stroke != tc.color || !got.Transparent {
				t.Fatalf("%s logo: %+v", tc.mode, got)
			}
		})
	}
}
