// Package useragent turns a User-Agent header into the few things a person
// recognises a sign-in by: the browser (or tool), the operating system and
// whether it's a phone. It's a small, offline best guess, not a database.
package useragent

import (
	"regexp"
	"strings"
)

// Info is what a User-Agent says about the client.
type Info struct {
	Browser string // "Chrome 131", "Firefox", "curl"; "" when unknown
	OS      string // "Windows", "Android 14", "iOS 18"; "" when unknown
	Mobile  bool   // a phone or tablet
	CLI     bool   // a script or command-line tool, not a browser
}

// Label is "Browser on OS", or whichever half is known.
func (i Info) Label() string {
	switch {
	case i.Browser != "" && i.OS != "":
		return i.Browser + " on " + i.OS
	case i.Browser != "":
		return i.Browser
	}
	return i.OS
}

type rule struct {
	re   *regexp.Regexp
	name string
}

// Tools that call the API directly. Checked first: some carry browser-like words.
var tools = []rule{
	{regexp.MustCompile(`(?i)^curl/`), "curl"},
	{regexp.MustCompile(`(?i)^wget/`), "Wget"},
	{regexp.MustCompile(`(?i)^go-http-client/`), "Go HTTP client"},
	{regexp.MustCompile(`(?i)^python-(requests|urllib|httpx)`), "Python"},
	{regexp.MustCompile(`(?i)^python/`), "Python"},
	{regexp.MustCompile(`(?i)powershell`), "PowerShell"},
	{regexp.MustCompile(`(?i)^httpie/`), "HTTPie"},
	{regexp.MustCompile(`(?i)^(node-fetch|undici|axios)`), "Node.js"},
	{regexp.MustCompile(`(?i)^node$`), "Node.js"},
	{regexp.MustCompile(`(?i)^cap/`), "cap"},
}

// Browsers, most specific first (most of them also say "Chrome" or "Safari").
var browsers = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`\bEdg(?:e|A|iOS)?/(\d+)`), "Edge"},
	{regexp.MustCompile(`\b(?:OPR|Opera)/(\d+)`), "Opera"},
	{regexp.MustCompile(`\bSamsungBrowser/(\d+)`), "Samsung Internet"},
	{regexp.MustCompile(`\bVivaldi/(\d+)`), "Vivaldi"},
	{regexp.MustCompile(`\b(?:Firefox|FxiOS)/(\d+)`), "Firefox"},
	{regexp.MustCompile(`; wv\).*\bChrome/(\d+)`), "Android WebView"},
	{regexp.MustCompile(`\b(?:Chrome|CriOS)/(\d+)`), "Chrome"},
	{regexp.MustCompile(`\bVersion/(\d+).*\bSafari/`), "Safari"},
	{regexp.MustCompile(`\bokhttp/(\d+)`), "NoCap OS app"},
}

var (
	reWindows = regexp.MustCompile(`Windows NT (\d+\.\d+)`)
	reAndroid = regexp.MustCompile(`Android (\d+)`)
	reIOS     = regexp.MustCompile(`(?:iPhone|CPU) OS (\d+)`)
)

// Parse reads a User-Agent header.
func Parse(ua string) Info {
	ua = strings.TrimSpace(ua)
	var i Info
	if ua == "" {
		return i
	}
	for _, t := range tools {
		if t.re.MatchString(ua) {
			i.Browser, i.CLI = t.name, true
			i.OS = osOf(ua)
			return i
		}
	}
	for _, b := range browsers {
		if m := b.re.FindStringSubmatch(ua); m != nil {
			i.Browser = b.name
			if len(m) > 1 && m[1] != "" && b.name != "Android WebView" && b.name != "NoCap OS app" {
				i.Browser += " " + m[1]
			}
			break
		}
	}
	i.OS = osOf(ua)
	i.Mobile = strings.Contains(ua, "Mobi") || strings.Contains(ua, "Android") ||
		strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad")
	return i
}

func osOf(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		if m := reWindows.FindStringSubmatch(ua); m != nil {
			switch m[1] {
			case "6.1":
				return "Windows 7"
			case "6.2", "6.3":
				return "Windows 8"
			}
		}
		return "Windows" // 10 and 11 both say NT 10.0
	case strings.Contains(ua, "Android"):
		if m := reAndroid.FindStringSubmatch(ua); m != nil {
			return "Android " + m[1]
		}
		return "Android"
	case strings.Contains(ua, "iPad"):
		if m := reIOS.FindStringSubmatch(ua); m != nil {
			return "iPadOS " + m[1]
		}
		return "iPadOS"
	case strings.Contains(ua, "iPhone"):
		if m := reIOS.FindStringSubmatch(ua); m != nil {
			return "iOS " + m[1]
		}
		return "iOS"
	case strings.Contains(ua, "CrOS"):
		return "ChromeOS"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		return "macOS"
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "X11"):
		return "Linux"
	}
	return ""
}
