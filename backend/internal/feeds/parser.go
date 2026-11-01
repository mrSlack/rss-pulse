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
	// About is the RSS 1.0 (RDF) item identifier, used as GUID fallback before the link.
	About          string `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# about,attr"`
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
	Summary   atomText   `xml:"summary"`
	Content   atomText   `xml:"content"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Links     []atomLink `xml:"link"`
}

// atomText is an Atom text construct. type="text" and type="html" carry escaped
// character data that is decoded once; type="xhtml" carries inline XHTML markup,
// which is kept as markup.
type atomText struct {
	Type  string `xml:"type,attr"`
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

func (t atomText) String() string {
	if strings.EqualFold(strings.TrimSpace(t.Type), "xhtml") {
		return unwrapXHTMLDiv(strings.TrimSpace(t.Inner))
	}
	return t.Text
}

// unwrapXHTMLDiv removes the single wrapping <div> that RFC 4287 requires around
// XHTML content, but only when it is a plain unprefixed div whose attributes are
// namespace declarations and that encloses the entire value.
func unwrapXHTMLDiv(inner string) string {
	decoder := xml.NewDecoder(strings.NewReader(inner))
	token, err := decoder.Token()
	if err != nil {
		return inner
	}
	start, ok := token.(xml.StartElement)
	if !ok || start.Name.Local != "div" || !strings.HasPrefix(inner, "<div") {
		return inner
	}
	for _, attribute := range start.Attr {
		if attribute.Name.Space != "xmlns" && !(attribute.Name.Space == "" && attribute.Name.Local == "xmlns") {
			return inner
		}
	}
	contentStart := decoder.InputOffset()
	for depth := 1; depth > 0; {
		token, err := decoder.Token()
		if err != nil {
			return inner
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	if decoder.InputOffset() != int64(len(inner)) || !strings.HasSuffix(inner, "</div>") {
		return inner
	}
	contentEnd := int64(len(inner) - len("</div>"))
	if contentEnd < contentStart {
		return ""
	}
	return strings.TrimSpace(inner[contentStart:contentEnd])
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
			document.Items = appendItem(document.Items, firstNonEmpty(rawItem.GUID, rawItem.About), rawItem.Title, rawItem.Link, firstNonEmpty(rawItem.ContentEncoded, rawItem.Description), firstNonEmpty(rawItem.Published, rawItem.Updated), base)
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
			document.Items = appendItem(document.Items, rawItem.ID, rawItem.Title, link, firstNonEmpty(rawItem.Summary.String(), rawItem.Content.String()), firstNonEmpty(rawItem.Published, rawItem.Updated), base)
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

// rfc822Zones maps the zone names allowed by RFC 822 to numeric offsets. Go's
// time package only knows the offset of a named zone when it is the local zone,
// so these names are rewritten before parsing.
var rfc822Zones = map[string]string{
	"UT": "+0000", "UTC": "+0000", "GMT": "+0000", "Z": "+0000",
	"EST": "-0500", "EDT": "-0400",
	"CST": "-0600", "CDT": "-0500",
	"MST": "-0700", "MDT": "-0600",
	"PST": "-0800", "PDT": "-0700",
}

// dateLayouts covers RFC 3339/ISO 8601 and RFC 822/1123 dates as they appear in
// real feeds: one- or two-digit days, optional weekday and seconds, two- or
// four-digit years, and numeric offsets with or without a colon. Layouts
// without a zone are interpreted as UTC.
var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 -07:00",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 06 15:04:05 -0700",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 MST",
	"Monday, 02-Jan-06 15:04:05 MST",
}

func parseDate(value string) (time.Time, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return time.Time{}, false
	}
	if offset, ok := rfc822Zones[strings.ToUpper(fields[len(fields)-1])]; ok && len(fields) > 1 {
		fields[len(fields)-1] = offset
	}
	value = strings.Join(fields, " ")
	for _, layout := range dateLayouts {
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
