package useragent

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		ua    string
		want  Info
		label string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			Info{Browser: "Chrome 131", OS: "Windows"}, "Chrome 131 on Windows"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
			Info{Browser: "Edge 131", OS: "Windows"}, "Edge 131 on Windows"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
			Info{Browser: "Firefox 133", OS: "Linux"}, "Firefox 133 on Linux"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
			Info{Browser: "Safari 18", OS: "macOS"}, "Safari 18 on macOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
			Info{Browser: "Safari 18", OS: "iOS 18", Mobile: true}, "Safari 18 on iOS 18"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
			Info{Browser: "Chrome 131", OS: "Android 14", Mobile: true}, "Chrome 131 on Android 14"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/131.0.0.0 Mobile Safari/537.36",
			Info{Browser: "Android WebView", OS: "Android 14", Mobile: true}, "Android WebView on Android 14"},
		{"Mozilla/5.0 (Linux; Android 13; SM-S911B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/23.0 Chrome/115.0.0.0 Mobile Safari/537.36",
			Info{Browser: "Samsung Internet 23", OS: "Android 13", Mobile: true}, "Samsung Internet 23 on Android 13"},
		{"curl/8.5.0", Info{Browser: "curl", CLI: true}, "curl"},
		{"Go-http-client/1.1", Info{Browser: "Go HTTP client", CLI: true}, "Go HTTP client"},
		{"python-requests/2.31.0", Info{Browser: "Python", CLI: true}, "Python"},
		{"Mozilla/5.0 (Windows NT 10.0; Microsoft Windows 10.0.26200; en-US) PowerShell/7.4.6",
			Info{Browser: "PowerShell", OS: "Windows", CLI: true}, "PowerShell on Windows"},
		{"", Info{}, ""},
		{"something odd", Info{}, ""},
	}
	for _, c := range cases {
		got := Parse(c.ua)
		if got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.ua, got, c.want)
		}
		if got.Label() != c.label {
			t.Errorf("Label(%q) = %q, want %q", c.ua, got.Label(), c.label)
		}
	}
}
