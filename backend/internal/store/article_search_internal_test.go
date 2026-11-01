package store

import (
	"strconv"
	"testing"
)

func TestSearchConditionUsesTrigramIndexOnlyForThreeCharacterKeys(t *testing.T) {
	const key = "COALESCE(a.search_key, pulse_article_search_key(a.title, a.summary))"
	const exact = "STRPOS(" + key + ", $1) > 0"
	const indexed = key + ` LIKE $1 ESCAPE '\'`
	cases := []struct {
		query, condition, value string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{"ǂ", "", ""},
		{"ma", exact, "ma"},
		{"%", exact, "%"},
		{`c:\t`, exact, `c:\t`},
		{". ,", exact, ".ǂ,"},
		{"MAR", indexed, "%mar%"},
		{"Größe", indexed, "%größe%"},
		// The separator is a letter, so short words of a phrase form trigrams together.
		{"A b", indexed, "%aǂb%"},
		{"ёж и", indexed, "%ёжǂи%"},
		{"Energy  Climate", indexed, "%energyǂclimate%"},
		{"energyǂclimate", indexed, "%energyǂclimate%"},
		{"Ёжик", indexed, "%ёжик%"},
		{"100%", indexed, `%100\%%`},
		{`sure_thing  \x`, indexed, `%sure\_thingǂ\\x%`},
	}
	for _, tc := range cases {
		var values []string
		add := func(value any) string {
			values = append(values, value.(string))
			return "$" + strconv.Itoa(len(values))
		}
		condition := searchCondition(tc.query, add)
		if condition != tc.condition {
			t.Errorf("searchCondition(%q) = %q, want %q", tc.query, condition, tc.condition)
		}
		if tc.condition == "" {
			if len(values) != 0 {
				t.Errorf("searchCondition(%q) added %q", tc.query, values)
			}
			continue
		}
		if len(values) != 1 || values[0] != tc.value {
			t.Errorf("searchCondition(%q) values = %q, want %q", tc.query, values, tc.value)
		}
	}
}
