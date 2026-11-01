// Package opml reads and writes OPML 2.0 subscription lists.
package opml

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxOutlines caps the number of outline elements in an imported document.
	MaxOutlines = 1000
	// MaxDepth caps how deeply outline elements may be nested.
	MaxDepth = 5
	// MaxFolderNameLength is the longest folder name (in characters) an
	// import maps to a folder; longer group names import without a folder.
	MaxFolderNameLength = 60
)

// ErrInvalid reports a document that is not well-formed OPML or exceeds the
// outline count or nesting limits.
var ErrInvalid = errors.New("invalid OPML document")

// Feed is one subscription found in an OPML document.
type Feed struct {
	URL   string
	Title string
	// Folder is the text of the nearest enclosing outline without an xmlUrl,
	// or "" when there is none or its name is not a valid folder name.
	Folder string
}

// Parse returns the feeds of an OPML document: every outline with an xmlUrl
// attribute inside <body>. It streams tokens so the outline count and nesting
// depth are enforced before a large tree is built. encoding/xml never expands
// external entities or loads DTDs.
func Parse(reader io.Reader) ([]Feed, error) {
	decoder := xml.NewDecoder(reader)
	var (
		stack    []openOutline
		feeds    []Feed
		outlines int
		sawRoot  bool
		inBody   bool
		// open records, for every element open inside <body>, whether it is
		// an outline, so each end tag pops the outline stack only when it
		// closes an outline, whatever wrapper elements surround it.
		open  []bool
		depth int // element depth in the document
	)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrInvalid
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			name := strings.ToLower(element.Name.Local)
			if depth == 1 {
				if name != "opml" {
					return nil, ErrInvalid
				}
				sawRoot = true
				continue
			}
			if !inBody {
				if depth == 2 && name == "body" {
					inBody = true
				}
				continue
			}
			if name != "outline" {
				open = append(open, false)
				continue
			}
			open = append(open, true)
			outlines++
			if outlines > MaxOutlines || len(stack) >= MaxDepth {
				return nil, ErrInvalid
			}
			xmlURL := strings.TrimSpace(attribute(element, "xmlUrl"))
			text := strings.TrimSpace(attribute(element, "text"))
			title := strings.TrimSpace(attribute(element, "title"))
			if title == "" {
				title = text
			}
			if text == "" {
				text = title
			}
			if xmlURL != "" {
				feeds = append(feeds, Feed{URL: xmlURL, Title: title, Folder: enclosingFolder(stack)})
				stack = append(stack, openOutline{isFeed: true})
			} else {
				stack = append(stack, openOutline{name: text})
			}
		case xml.EndElement:
			depth--
			if !inBody {
				continue
			}
			if depth == 1 {
				inBody = false
				continue
			}
			isOutline := open[len(open)-1]
			open = open[:len(open)-1]
			if isOutline {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !sawRoot {
		return nil, ErrInvalid
	}
	return feeds, nil
}

// openOutline is an outline element whose end tag has not been read yet.
type openOutline struct {
	isFeed bool
	name   string
}

// enclosingFolder returns the name of the nearest enclosing group outline.
func enclosingFolder(stack []openOutline) string {
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].isFeed {
			continue
		}
		name := stack[index].name
		if name == "" || utf8.RuneCountInString(name) > MaxFolderNameLength {
			return ""
		}
		return name
	}
	return ""
}

// attribute returns the value of the named attribute, matched without regard
// to case because some exporters write xmlurl.
func attribute(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Space == "" && strings.EqualFold(attr.Name.Local, name) {
			return attr.Value
		}
	}
	return ""
}

// Subscription is one feed to export.
type Subscription struct {
	Title string
	URL   string
}

// Group is a folder of subscriptions; a Group with an empty Name holds
// subscriptions without a folder, which are written at the top level.
type Group struct {
	Name          string
	Subscriptions []Subscription
}

type outlineXML struct {
	Text     string       `xml:"text,attr"`
	Title    string       `xml:"title,attr,omitempty"`
	Type     string       `xml:"type,attr,omitempty"`
	XMLURL   string       `xml:"xmlUrl,attr,omitempty"`
	Outlines []outlineXML `xml:"outline"`
}

type documentXML struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    struct {
		Title       string `xml:"title"`
		DateCreated string `xml:"dateCreated"`
	} `xml:"head"`
	Body struct {
		Outlines []outlineXML `xml:"outline"`
	} `xml:"body"`
}

// Write encodes groups as an OPML 2.0 document.
func Write(writer io.Writer, title string, created time.Time, groups []Group) error {
	var document documentXML
	document.Version = "2.0"
	document.Head.Title = title
	document.Head.DateCreated = created.UTC().Format(time.RFC1123Z)
	document.Body.Outlines = []outlineXML{}
	feedOutline := func(subscription Subscription) outlineXML {
		return outlineXML{Text: subscription.Title, Title: subscription.Title, Type: "rss", XMLURL: subscription.URL}
	}
	for _, group := range groups {
		if group.Name == "" {
			for _, subscription := range group.Subscriptions {
				document.Body.Outlines = append(document.Body.Outlines, feedOutline(subscription))
			}
			continue
		}
		folder := outlineXML{Text: group.Name, Title: group.Name, Outlines: []outlineXML{}}
		for _, subscription := range group.Subscriptions {
			folder.Outlines = append(folder.Outlines, feedOutline(subscription))
		}
		document.Body.Outlines = append(document.Body.Outlines, folder)
	}
	if _, err := io.WriteString(writer, xml.Header); err != nil {
		return err
	}
	encoder := xml.NewEncoder(writer)
	encoder.Indent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "\n")
	return err
}
