package store

import (
	"html"
	"strings"
	"unicode"
)

// SearchWordSeparator joins the words of a search key. It is a letter (U+01C2 LATIN
// LETTER ALVEOLAR CLICK) rather than a space so that pg_trgm, which extracts trigrams
// from runs of letters and digits, treats the whole key as one word and also indexes the
// trigrams that span word boundaries ("yǂc" and "ǂcl" for "energy climate"). Trigrams of
// separate words cannot express adjacency: an index over space-separated text returned
// every article containing all words of a phrase as a candidate, which took seconds for
// phrases of common words. The character is replaced by a space in article text and in
// queries before they are split into words, so it only ever stands for a word boundary.
const SearchWordSeparator = "ǂ"

// ArticleSearchKey returns the value stored in articles.search_key: the words of the
// title and of the plain text of the summary (tags removed, entities decoded),
// lower-cased and joined by SearchWordSeparator. Search matches the key of the query as
// a substring of this key, so markup such as attribute values or tag names never matches.
func ArticleSearchKey(title, summary string) string {
	return searchKey(title + " " + html.UnescapeString(stripTags(summary)))
}

// searchKey lower-cases value and joins its whitespace-separated words with
// SearchWordSeparator.
func searchKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(value, SearchWordSeparator, " "))), SearchWordSeparator)
}

// articleSearchKey is the key search matches: the stored search_key or, for a row without
// it (not backfilled yet, see ArticleSearchUpgrade, or inserted without it), the SQL
// version pulse_article_search_key of migration 018. The trigram index
// articles_search_key_trgm_idx covers this expression.
var articleSearchKey = searchKeyExpression("a.")

func searchKeyExpression(prefix string) string {
	return "COALESCE(" + prefix + "search_key, pulse_article_search_key(" + prefix + "title, " + prefix + "summary))"
}

// searchCondition returns the SQL condition that matches the key of the query as a
// literal substring of the article search key, passing the value through add, or "" for
// a query without searchable characters. LIKE (with %, _, and \ escaped) lets the
// trigram index find candidates. pg_trgm extracts no trigram from a pattern without
// three consecutive letters or digits (the separator counts as a letter), and such a
// LIKE would read the whole index, so these queries use STRPOS, which only scans the
// user's articles.
func searchCondition(query string, add func(any) string) string {
	key := searchKey(query)
	if key == "" {
		return ""
	}
	if !hasTrigramWord(key) {
		return "STRPOS(" + articleSearchKey + ", " + add(key) + ") > 0"
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(key)
	return articleSearchKey + " LIKE " + add("%"+escaped+"%") + ` ESCAPE '\'`
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

// stripTags replaces every complete <...> sequence with a space, like pulse_article_search_key's
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
