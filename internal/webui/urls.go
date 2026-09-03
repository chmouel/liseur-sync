package webui

import "net/url"

// The UI has one rule about links, and this file is where it lives.
//
// Every URL a handler puts in a view is a path relative to the UI root
// (/ui/), with no leading slash: "library", "books/{id}",
// "entities/series/{id}". A template renders it as `prefix + path`,
// where prefix is relPrefix of the page's own request path. That pairing
// is what lets the whole UI work under a stripped subpath, and it only
// holds if both halves agree — a value that is already relative to the
// page, or that still carries a route shape from an older layout, points
// somewhere that does not exist.
//
// It went wrong exactly that way once: entities became library-wide
// (ADR-0019) and moved from /ui/folders/{folder}/{kind}/{entity} to
// /ui/entities/{kind}/{entity}, the templates were updated and these
// builders were not, so every author and series chip on a book page led
// to a 404. Routes move; a builder in one place moves with them.

// uiEntityList is the page listing every entity of one kind.
func uiEntityList(kind string) string {
	return "entities/" + url.PathEscape(kind)
}

// uiEntity is the page for one entity: a contributor's or a tag's
// listing, or a series' shelf, which is a route of its own (ADR-0018)
// under the same path.
func uiEntity(kind, entityID string) string {
	return uiEntityList(kind) + "/" + url.PathEscape(entityID)
}
