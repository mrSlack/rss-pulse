package store

import (
	"html"
	"strings"
	"unicode"
)

// ArticleSearchText returns the value stored in articles.search_text: the title and the
// plain text of the summary (tags removed, entities decoded), lower-cased with runs of
// whitespace collapsed. Search matches lower-cased queries as substrings of this text,
// so markup such as attribute values or tag names never matches.
func ArticleSearchText(title, summary string) string {
	return normalizeSearchText(title + " " + html.UnescapeString(stripTags(summary)))
}

// searchCondition returns the SQL condition that matches the normalized query as a
// literal substring of a.search_text, passing the value through add, or "" for a query
// without searchable characters. LIKE (with %, _, and \ escaped) lets the trigram index
// articles_search_text_trgm_idx find candidates. pg_trgm extracts no trigram from a
// pattern whose words are all shorter than three letters or digits, and such a LIKE
// would read the whole index, so these queries use STRPOS, which only scans the user's
// articles.
func searchCondition(query string, add func(any) string) string {
	normalized := normalizeSearchText(query)
	if normalized == "" {
		return ""
	}
	if !hasTrigramWord(normalized) {
		return "STRPOS(a.search_text, " + add(normalized) + ") > 0"
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(normalized)
	return "a.search_text LIKE " + add("%"+escaped+"%") + ` ESCAPE '\'`
}

// hasTrigramWord reports whether value contains at least three consecutive letters or
// digits, the shortest word pg_trgm extracts a trigram from.
func hasTrigramWord(value string) bool {
	run := 0
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			run = 0
			continue
		}
		run++
		if run >= 3 {
			return true
		}
	}
	return false
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
