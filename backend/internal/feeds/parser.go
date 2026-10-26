package feeds

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/url"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
)

type Item struct {
	GUID           string
	Title          string
	URL            string
	Summary        string
	PublishedAt    time.Time
	HasPublishedAt bool
}

type Document struct {
	Title   string
	SiteURL string
	Items   []Item
}

type rawFeed struct {
	XMLName  xml.Name
	Title    string      `xml:"title"`
	Links    []atomLink  `xml:"link"`
	Channel  rssChannel  `xml:"channel"`
	Entries  []atomEntry `xml:"entry"`
	RDFItems []rssItem   `xml:"item"`
}

type rssChannel struct {
	Title string    `xml:"title"`
	Link  string    `xml:"link"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	GUID           string `xml:"guid"`
	Title          string `xml:"title"`
	Link           string `xml:"link"`
	Description    string `xml:"description"`
	ContentEncoded string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Published      string `xml:"pubDate"`
	Updated        string `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Links     []atomLink `xml:"link"`
}

func Parse(data []byte, base *url.URL) (Document, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		encoding, err := htmlindex.Get(label)
		if err != nil {
			return nil, err
		}
		return encoding.NewDecoder().Reader(input), nil
	}
	var raw rawFeed
	if err := decoder.Decode(&raw); err != nil {
		return Document{}, errors.New("the response is not a valid RSS or Atom feed")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Document{}, errors.New("the RSS feed contains trailing or malformed data")
	}

	title := strings.TrimSpace(raw.Channel.Title)
	if title == "" {
		title = strings.TrimSpace(raw.Title)
	}
	siteLink := strings.TrimSpace(raw.Channel.Link)
	if siteLink == "" {
		for _, link := range raw.Links {
			if link.Rel == "alternate" || link.Rel == "" {
				siteLink = link.Href
				break
			}
		}
	}
	document := Document{Title: title, SiteURL: resolve(base, siteLink)}

	switch strings.ToLower(raw.XMLName.Local) {
	case "rss":
		if strings.TrimSpace(raw.Channel.Title) == "" {
			return Document{}, errors.New("the RSS feed is missing a channel title")
		}
		for _, rawItem := range raw.Channel.Items {
			document.Items = appendItem(document.Items, rawItem.GUID, rawItem.Title, rawItem.Link, firstNonEmpty(rawItem.ContentEncoded, rawItem.Description), firstNonEmpty(rawItem.Published, rawItem.Updated), base)
		}
	case "rdf":
		if strings.TrimSpace(raw.Channel.Title) == "" {
			return Document{}, errors.New("the RSS feed is missing a channel title")
		}
		for _, rawItem := range raw.RDFItems {
			document.Items = appendItem(document.Items, rawItem.GUID, rawItem.Title, rawItem.Link, firstNonEmpty(rawItem.ContentEncoded, rawItem.Description), firstNonEmpty(rawItem.Published, rawItem.Updated), base)
		}
	case "feed":
		if strings.TrimSpace(raw.Title) == "" {
			return Document{}, errors.New("the Atom feed is missing a title")
		}
		for _, rawItem := range raw.Entries {
			link := ""
			for _, candidate := range rawItem.Links {
				if candidate.Rel == "alternate" || candidate.Rel == "" {
					link = candidate.Href
					break
				}
			}
			document.Items = appendItem(document.Items, rawItem.ID, rawItem.Title, link, firstNonEmpty(rawItem.Summary, rawItem.Content), firstNonEmpty(rawItem.Published, rawItem.Updated), base)
		}
	default:
		return Document{}, errors.New("the source does not contain an RSS or Atom feed")
	}
	if len(document.Items) > 200 {
		document.Items = document.Items[:200]
	}
	return document, nil
}

func appendItem(items []Item, guid, title, itemURL, summary, published string, base *url.URL) []Item {
	title = strings.TrimSpace(title)
	if title == "" {
		return items
	}
	resolvedURL := resolve(base, strings.TrimSpace(itemURL))
	guid = strings.TrimSpace(guid)
	if guid == "" {
		guid = resolvedURL
	}
	if guid == "" {
		guid = title + "|" + strings.TrimSpace(published)
	}
	publishedAt, hasPublishedAt := parseDate(published)
	if !hasPublishedAt {
		publishedAt = time.Now().UTC()
	}
	return append(items, Item{
		GUID:           limitRunes(guid, 1500),
		Title:          limitRunes(html.UnescapeString(title), 500),
		URL:            resolvedURL,
		Summary:        limitRunes(strings.TrimSpace(summary), 20000),
		PublishedAt:    publishedAt,
		HasPublishedAt: hasPublishedAt,
	})
}

func resolve(base *url.URL, raw string) string {
	if raw == "" || base == nil {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	return resolved.String()
}

func parseDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func limitRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
