package store

import "testing"

func TestSearchConditionUsesTrigramIndexOnlyForThreeCharacterWords(t *testing.T) {
	cases := []struct {
		query, condition, value string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{"ma", "STRPOS(a.search_text, $1) > 0", "ma"},
		{"A b", "STRPOS(a.search_text, $1) > 0", "a b"},
		{"%", "STRPOS(a.search_text, $1) > 0", "%"},
		{`c:\t`, "STRPOS(a.search_text, $1) > 0", `c:\t`},
		{"MAR", `a.search_text LIKE $1 ESCAPE '\'`, "%mar%"},
		{"Größe", `a.search_text LIKE $1 ESCAPE '\'`, "%größe%"},
		{"ёж и", "STRPOS(a.search_text, $1) > 0", "ёж и"},
		{"Ёжик", `a.search_text LIKE $1 ESCAPE '\'`, "%ёжик%"},
		{"100%", `a.search_text LIKE $1 ESCAPE '\'`, `%100\%%`},
		{`sure_thing  \x`, `a.search_text LIKE $1 ESCAPE '\'`, `%sure\_thing \\x%`},
	}
	for _, tc := range cases {
		var values []any
		add := func(value any) string {
			values = append(values, value)
			return "$1"
		}
		condition := searchCondition(tc.query, add)
		if condition != tc.condition {
			t.Errorf("searchCondition(%q) = %q, want %q", tc.query, condition, tc.condition)
		}
		if tc.condition == "" {
			if len(values) != 0 {
				t.Errorf("searchCondition(%q) added %v", tc.query, values)
			}
			continue
		}
		if len(values) != 1 || values[0] != tc.value {
			t.Errorf("searchCondition(%q) value = %v, want %q", tc.query, values, tc.value)
		}
	}
}
