package feeds

import (
	"errors"
	"html"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var ErrInvalidStopWord = errors.New("stop word must contain between 1 and 80 letters or digits")

// NormalizeStopWord makes words and phrases comparable across markup and spacing.
func NormalizeStopWord(value string) (string, error) {
	normalized := normalizeWords(value)
	if normalized == "" || len([]rune(normalized)) > 80 {
		return "", ErrInvalidStopWord
	}
	return normalized, nil
}

// FindStopWordMatch returns the first configured word or phrase found in text.
func FindStopWordMatch(text string, terms []string) string {
	words := normalizeWords(text)
	if words == "" {
		return ""
	}
	for _, term := range terms {
		if term == "" {
			continue
		}
		if containsPhrase(words, term) {
			return term
		}
	}
	return ""
}

func normalizeWords(value string) string {
	value = norm.NFKC.String(html.UnescapeString(value))
	var result strings.Builder
	space := true
	inTag := false
	for _, r := range strings.ToLower(value) {
		if r == '<' {
			inTag = true
			if !space {
				result.WriteByte(' ')
				space = true
			}
			continue
		}
		if inTag {
			if r == '>' {
				inTag = false
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			result.WriteRune(r)
			space = false
		} else if !space {
			result.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(result.String())
}

func containsPhrase(text, phrase string) bool {
	textRunes := []rune(" " + text + " ")
	phraseRunes := []rune(" " + phrase + " ")
	if len(phraseRunes) > len(textRunes) {
		return false
	}
	for i := 0; i <= len(textRunes)-len(phraseRunes); i++ {
		match := true
		for j := range phraseRunes {
			if textRunes[i+j] != phraseRunes[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
