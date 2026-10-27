package grade

import (
	"reflect"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com/docs", "https://example.com/docs"},
		{"HTTPS://WWW.Example.COM/Docs/", "https://example.com/Docs"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"https://example.com/a/b///", "https://example.com/a/b"},
		{"https://example.com/a#section-2", "https://example.com/a"},
		{"https://example.com/a?utm_source=x&utm_medium=y", "https://example.com/a"},
		{"https://example.com/a?UTM_Campaign=x&id=7", "https://example.com/a?id=7"},
		{"https://example.com/a?fbclid=abc&gclid=def&ref=home", "https://example.com/a"},
		{"https://example.com/a?ref_src=x", "https://example.com/a?ref_src=x"},
		{"https://example.com/a?z=1&a=2&m=3", "https://example.com/a?a=2&m=3&z=1"},
		{"https://example.com/a?tag=b&tag=a", "https://example.com/a?tag=a&tag=b"},
		{"https://www.example.com/a/?b=2&utm_source=n&a=1#top", "https://example.com/a?a=1&b=2"},
		{"http://example.com/a", "http://example.com/a"},
		{"https://docs.example.com:8443/a/", "https://docs.example.com:8443/a"},
		{"  https://example.com/a/  ", "https://example.com/a"},
		{"not a url", "not a url"},
		{"example.com/a", "example.com/a"},
	}
	for _, c := range cases {
		if got := NormalizeURL(c.in); got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeURLIdempotent(t *testing.T) {
	for _, in := range []string{
		"HTTPS://WWW.Example.COM/Docs/?b=2&utm_source=n&a=1#top",
		"https://example.com/a?tag=b&tag=a",
		"https://example.com/",
	} {
		once := NormalizeURL(in)
		if twice := NormalizeURL(once); twice != once {
			t.Errorf("NormalizeURL not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}

func TestCitedURLs(t *testing.T) {
	text := "See [pricing](https://www.example.com/pricing/) and <https://blog.example.org/post?utm_source=x>.\n" +
		"Also (https://docs.example.com/a_(b)), https://example.com/pricing#faq, and **https://x.dev/go**!\n" +
		"Not a URL: example.com/nothing or ftp://old.example.com/file."
	want := []string{
		"https://example.com/pricing",
		"https://blog.example.org/post",
		"https://docs.example.com/a_(b)",
		"https://x.dev/go",
	}
	if got := citedURLs(text); !reflect.DeepEqual(got, want) {
		t.Errorf("citedURLs = %q, want %q", got, want)
	}
}
