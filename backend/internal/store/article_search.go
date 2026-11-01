package store

import (
	"html"
	"strings"
)

// ArticleSearchText returns the value stored in articles.search_text: the title and the
// plain text of the summary (tags removed, entities decoded), lower-cased with runs of
// whitespace collapsed. Search matches lower-cased queries as substrings of this text,
// so markup such as attribute values or tag names never matches.
func ArticleSearchText(title, summary string) string {
	return normalizeSearchText(title + " " + html.UnescapeString(stripTags(summary)))
}

// searchPattern turns a user query into a LIKE pattern (escape character '\') that
// matches the normalized query as a literal substring of search_text. It returns ""
// for a query without searchable characters.
func searchPattern(query string) string {
	normalized := normalizeSearchText(query)
	if normalized == "" {
		return ""
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(normalized)
	return "%" + escaped + "%"
}

func normalizeSearchText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

// stripTags replaces every complete <...> sequence with a space, like the SQL backfill's
// regexp_replace(summary, '<[^>]*>', ' ', 'g'); a '<' without a closing '>' is kept as text.
func stripTags(value string) string {
	if !strings.Contains(value, "<") {
		return value
	}
	var result strings.Builder
	result.Grow(len(value))
	for {
		start := strings.IndexByte(value, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(value[start:], '>')
		if end < 0 {
			break
		}
		result.WriteString(value[:start])
		result.WriteByte(' ')
		value = value[start+end+1:]
	}
	result.WriteString(value)
	return result.String()
}
