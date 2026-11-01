package store_test

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/store"
)

func TestArticleCursorRoundTripKeepsMicrosecondsAndID(t *testing.T) {
	cursor := store.ArticleCursor{PublishedAt: time.Date(2026, 10, 31, 23, 59, 58, 123456000, time.UTC), ID: 4242}
	encoded := store.EncodeArticleCursor(cursor)
	decoded, err := store.DecodeArticleCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeArticleCursor(%q) error = %v", encoded, err)
	}
	if !decoded.PublishedAt.Equal(cursor.PublishedAt) || decoded.ID != cursor.ID {
		t.Fatalf("decoded cursor = %+v, want %+v", decoded, cursor)
	}
	before := store.ArticleCursor{PublishedAt: time.Date(1965, 1, 2, 3, 4, 5, 6000, time.UTC), ID: 1}
	decoded, err = store.DecodeArticleCursor(store.EncodeArticleCursor(before))
	if err != nil || !decoded.PublishedAt.Equal(before.PublishedAt) || decoded.ID != 1 {
		t.Fatalf("pre-epoch cursor round trip = (%+v, %v), want %+v", decoded, err, before)
	}
}

func TestArticleCursorRejectsMalformedInput(t *testing.T) {
	raw := func(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }
	valid := store.EncodeArticleCursor(store.ArticleCursor{PublishedAt: time.Unix(1700000000, 0), ID: 7})
	cases := map[string]string{
		"empty":            "",
		"not base64":       "%%%",
		"padded base64":    valid + "=",
		"standard base64":  base64.StdEncoding.EncodeToString([]byte("1700000000000000:77")),
		"missing id":       raw("1700000000000000"),
		"missing time":     raw(":7"),
		"zero id":          raw("1700000000000000:0"),
		"negative id":      raw("1700000000000000:-7"),
		"plus sign":        raw("+1700000000000000:7"),
		"leading zero id":  raw("1700000000000000:07"),
		"extra field":      raw("1700000000000000:7:1"),
		"non numeric":      raw("yesterday:7"),
		"whitespace":       raw("1700000000000000: 7"),
		"overflowing id":   raw("1700000000000000:99999999999999999999"),
		"overflowing time": raw("99999999999999999999:7"),
		"too long":         raw("1700000000000000:7" + string(make([]byte, 80))),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.DecodeArticleCursor(input); !errors.Is(err, store.ErrInvalidCursor) {
				t.Fatalf("DecodeArticleCursor(%q) error = %v, want ErrInvalidCursor", input, err)
			}
		})
	}
}
