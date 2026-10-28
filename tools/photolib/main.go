// Command photolib builds the built-in background photos embedded by
// internal/service/imagegen/photos from Wikimedia Commons.
//
// photos.json lists each photo's Commons file title. For every entry the
// tool asks the Commons API for the file's licence, refuses anything that is
// not CC0, downloads a 1280-pixel rendition, crops and scales it to the
// library size and writes NN.jpg next to the list. It also rewrites the
// list with the author and page URL the API reports, so the record of where
// each photo came from is never typed by hand.
//
//	go run ./tools/photolib
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

const (
	api       = "https://commons.wikimedia.org/w/api.php"
	userAgent = "BotGuard-photolib/1.0 (https://github.com/dkeepersun/BotGuard)"
)

// Photo is one entry of photos.json.
type Photo struct {
	File    string `json:"file"`
	Title   string `json:"title"`
	Page    string `json:"page,omitempty"`
	Author  string `json:"author,omitempty"`
	License string `json:"license,omitempty"`
}

var client = &http.Client{Timeout: 2 * time.Minute}

func main() {
	dir := flag.String("dir", "internal/service/imagegen/photos", "directory holding photos.json and the photos")
	flag.Parse()

	listPath := filepath.Join(*dir, "photos.json")
	raw, err := os.ReadFile(listPath)
	if err != nil {
		log.Fatal(err)
	}
	var photos []Photo
	if err := json.Unmarshal(raw, &photos); err != nil {
		log.Fatal(err)
	}
	keep := map[string]bool{"photos.json": true}
	for i := range photos {
		p := &photos[i]
		p.File = fmt.Sprintf("%02d.jpg", i+1)
		keep[p.File] = true
		if err := fetch(p, filepath.Join(*dir, p.File)); err != nil {
			log.Fatalf("%s: %v", p.Title, err)
		}
		log.Printf("%s  %s", p.File, p.Title)
		time.Sleep(time.Second)
	}
	// Photos dropped from the list are removed too.
	entries, err := os.ReadDir(*dir)
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		if !keep[e.Name()] && strings.HasSuffix(e.Name(), ".jpg") {
			if err := os.Remove(filepath.Join(*dir, e.Name())); err != nil {
				log.Fatal(err)
			}
		}
	}
	out, err := json.MarshalIndent(photos, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(listPath, append(out, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
}

type imageInfo struct {
	ThumbURL       string `json:"thumburl"`
	DescriptionURL string `json:"descriptionurl"`
	ExtMetadata    map[string]struct {
		Value string `json:"value"`
	} `json:"extmetadata"`
}

var tags = regexp.MustCompile(`<[^>]*>`)

func fetch(p *Photo, path string) error {
	q := url.Values{
		"action": {"query"}, "format": {"json"}, "titles": {p.Title},
		"prop": {"imageinfo"}, "iiprop": {"url|extmetadata"}, "iiurlwidth": {"1280"},
		"iiextmetadatafilter": {"LicenseShortName|Artist"},
	}
	var resp struct {
		Query struct {
			Pages map[string]struct {
				ImageInfo []imageInfo `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := getJSON(api+"?"+q.Encode(), &resp); err != nil {
		return err
	}
	var info *imageInfo
	for _, pg := range resp.Query.Pages {
		if len(pg.ImageInfo) == 1 {
			info = &pg.ImageInfo[0]
		}
	}
	if info == nil {
		return fmt.Errorf("not found on Commons")
	}
	if lic := info.ExtMetadata["LicenseShortName"].Value; lic != "CC0" {
		return fmt.Errorf("licence is %q, not CC0", lic)
	}
	p.License = "CC0"
	p.Page = info.DescriptionURL
	p.Author = strings.TrimSpace(tags.ReplaceAllString(info.ExtMetadata["Artist"].Value, ""))

	body, err := get(info.ThumbURL)
	if err != nil {
		return err
	}
	jpeg, err := imagegen.NormalizeAt(body, 80)
	if err != nil {
		return err
	}
	return os.WriteFile(path, jpeg, 0o644)
}

func get(u string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		res, err := client.Do(req)
		if err == nil && res.StatusCode == http.StatusOK {
			defer res.Body.Close()
			return io.ReadAll(io.LimitReader(res.Body, 32<<20))
		}
		if err == nil {
			res.Body.Close()
			err = fmt.Errorf("%s: %s", u, res.Status)
		}
		if attempt == 4 {
			return nil, err
		}
		time.Sleep(time.Duration(5*(attempt+1)) * time.Second)
	}
}

func getJSON(u string, v any) error {
	b, err := get(u)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
