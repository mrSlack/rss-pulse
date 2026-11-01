package store

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidCursor reports a malformed article paging cursor.
var ErrInvalidCursor = errors.New("invalid article cursor")

// maxEncodedCursorLength bounds the opaque cursor before decoding. A valid
// cursor encodes two signed 64-bit integers and a separator (at most 41 bytes).
const maxEncodedCursorLength = 64

// ArticleCursor identifies the position of an article in the article list
// order (published_at DESC, id DESC). A page requested with a cursor contains
// only articles strictly after that position.
type ArticleCursor struct {
	PublishedAt time.Time
	ID          int64
}

// EncodeArticleCursor returns an opaque, URL-safe cursor. PostgreSQL stores
// timestamps with microsecond precision, so microseconds are lossless.
func EncodeArticleCursor(cursor ArticleCursor) string {
	raw := strconv.FormatInt(cursor.PublishedAt.UnixMicro(), 10) + ":" + strconv.FormatInt(cursor.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeArticleCursor parses a cursor produced by EncodeArticleCursor. Any
// input that is not the canonical encoding of a positive article ID is
// rejected with ErrInvalidCursor.
func DecodeArticleCursor(encoded string) (ArticleCursor, error) {
	if encoded == "" || len(encoded) > maxEncodedCursorLength {
		return ArticleCursor{}, ErrInvalidCursor
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return ArticleCursor{}, ErrInvalidCursor
	}
	rawTime, rawID, found := strings.Cut(string(decoded), ":")
	if !found {
		return ArticleCursor{}, ErrInvalidCursor
	}
	micros, err := strconv.ParseInt(rawTime, 10, 64)
	if err != nil {
		return ArticleCursor{}, ErrInvalidCursor
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return ArticleCursor{}, ErrInvalidCursor
	}
	cursor := ArticleCursor{PublishedAt: time.UnixMicro(micros).UTC(), ID: id}
	// Requiring the canonical form rejects signs, leading zeros, padding and
	// other spellings that would otherwise decode to the same position.
	if EncodeArticleCursor(cursor) != encoded {
		return ArticleCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}
