package grade

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// urlRe finds http and https URLs in prose and markdown.
var urlRe = regexp.MustCompile("https?://[^\\s<>\"'`\\]]+")

// trackingParams are dropped on top of every utm_* parameter.
var trackingParams = map[string]bool{"fbclid": true, "gclid": true, "ref": true}

// NormalizeURL makes two spellings of the same page compare equal: lowercase
// scheme and host, no "www.", no fragment, no trailing slash, no tracking
// parameters (utm_*, fbclid, gclid, ref), and the remaining query parameters
// sorted. Text that isn't an absolute URL is returned trimmed.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	u.Fragment, u.RawFragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	u.RawQuery = cleanQuery(u.RawQuery)
	u.ForceQuery = false
	return u.String()
}

// cleanQuery drops tracking parameters and sorts what is left.
func cleanQuery(q string) string {
	if q == "" {
		return ""
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return q
	}
	for k := range vals {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "utm_") || trackingParams[lk] {
			delete(vals, k)
			continue
		}
		sort.Strings(vals[k])
	}
	return vals.Encode()
}

// citedURLs returns the normalized URLs in text, once each, in order.
func citedURLs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range urlRe.FindAllString(text, -1) {
		u := NormalizeURL(trimURL(m))
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// trimURL cuts the punctuation a sentence or markdown puts after a URL.
func trimURL(u string) string {
	for {
		t := strings.TrimRight(u, ".,;:!?*_")
		if strings.HasSuffix(t, ")") && strings.Count(t, ")") > strings.Count(t, "(") {
			t = t[:len(t)-1]
		}
		if t == u {
			return u
		}
		u = t
	}
}

// stripURLs blanks URLs, so their pieces aren't read as paths or numbers.
func stripURLs(text string) string { return urlRe.ReplaceAllString(text, " ") }
