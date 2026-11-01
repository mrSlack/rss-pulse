package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/pulse-reader/app/internal/opml"
	"github.com/pulse-reader/app/internal/store"
)

// maxOPMLBytes caps an imported OPML document.
const maxOPMLBytes = 1 << 20

// exportOPML handles GET /api/opml: the caller's subscriptions as an OPML 2.0
// download. Feeds without a folder come first, then one outline group per
// folder (in folder list order); feeds are sorted by their personal title.
func (h *Handler) exportOPML(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	folders, err := h.store.ListFoldersForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "opml.export_failed", "The subscriptions could not be exported.")
		return
	}
	feeds, err := h.store.ListFeedsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "opml.export_failed", "The subscriptions could not be exported.")
		return
	}
	sort.SliceStable(feeds, func(i, j int) bool { return strings.ToLower(feeds[i].Title) < strings.ToLower(feeds[j].Title) })
	byFolder := map[int64][]opml.Subscription{}
	var unfiled []opml.Subscription
	for _, feed := range feeds {
		subscription := opml.Subscription{Title: feed.Title, URL: feed.URL}
		if feed.FolderID == nil {
			unfiled = append(unfiled, subscription)
		} else {
			byFolder[*feed.FolderID] = append(byFolder[*feed.FolderID], subscription)
		}
	}
	groups := []opml.Group{{Subscriptions: unfiled}}
	for _, folder := range folders {
		if len(byFolder[folder.ID]) > 0 {
			groups = append(groups, opml.Group{Name: folder.Name, Subscriptions: byFolder[folder.ID]})
		}
	}
	var document bytes.Buffer
	if err := opml.Write(&document, "Pulse subscriptions", h.now(), groups); err != nil {
		writeError(w, http.StatusInternalServerError, "opml.export_failed", "The subscriptions could not be exported.")
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pulse-subscriptions.opml"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(document.Bytes())
}

// importOPML handles POST /api/opml with an OPML document as the raw body.
func (h *Handler) importOPML(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/x-opml" && mediaType != "application/xml" && mediaType != "text/xml") {
		writeError(w, http.StatusUnsupportedMediaType, "opml.content_type_invalid", "Upload an OPML file (text/x-opml or application/xml).")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOPMLBytes))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "opml.too_large", "The OPML file must not be larger than 1 MiB.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "opml.invalid", "The file is not a valid OPML subscription list.")
		return
	}
	feeds, err := opml.Parse(bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "opml.invalid", "The file is not a valid OPML subscription list.")
		return
	}
	imports := make([]store.SubscriptionImport, len(feeds))
	for index, feed := range feeds {
		imports[index] = store.SubscriptionImport{URL: feed.URL, Folder: feed.Folder}
	}
	user, _ := currentUser(r)
	result, err := h.store.ImportSubscriptions(r.Context(), user.ID, imports)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "opml.import_failed", "The subscriptions could not be imported.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
