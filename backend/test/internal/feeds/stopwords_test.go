package feeds_test

import (
	"testing"

	"github.com/pulse-reader/app/internal/feeds"
)

func TestFindStopWordMatchUsesWholeWordsAndNormalizedPhrases(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		terms []string
		want  string
	}{
		{name: "Russian term ignores case and markup", text: "<p>ЭРОТИКА для взрослых</p>", terms: []string{"эротика"}, want: "эротика"},
		{name: "English phrase tolerates punctuation", text: "An only-fans promotion", terms: []string{"only fans"}, want: "only fans"},
		{name: "word boundary avoids substring", text: "A classic assessment", terms: []string{"ass"}},
		{name: "HTML entity decodes", text: "adult&nbsp;content", terms: []string{"adult content"}, want: "adult content"},
		{name: "no configured terms", text: "ordinary news", terms: nil},
		{name: "term is the whole text", text: "Spam", terms: []string{"spam"}, want: "spam"},
		{name: "term at the end", text: "this is spam", terms: []string{"spam"}, want: "spam"},
		{name: "first configured term wins", text: "casino and lottery", terms: []string{"lottery", "casino"}, want: "lottery"},
		{name: "term longer than text", text: "hi", terms: []string{"hi there"}},
		{name: "Cyrillic phrase across punctuation", text: "Новое секс-видео", terms: []string{"секс видео"}, want: "секс видео"},
		{name: "Cyrillic word boundary", text: "сексуальность и сексология", terms: []string{"секс"}},
		{name: "empty term is ignored", text: "ordinary news", terms: []string{"", "news"}, want: "news"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := feeds.FindStopWordMatch(test.text, test.terms); got != test.want {
				t.Errorf("FindStopWordMatch() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeStopWordRejectsEmptyAndNormalizesPhrase(t *testing.T) {
	got, err := feeds.NormalizeStopWord("  ONLY&nbsp;FANS ")
	if err != nil || got != "only fans" {
		t.Fatalf("NormalizeStopWord() = %q, %v; want %q, nil", got, err, "only fans")
	}
	if _, err := feeds.NormalizeStopWord(" <b> </b> "); err == nil {
		t.Fatal("NormalizeStopWord() accepted a term without letters or digits")
	}
}
