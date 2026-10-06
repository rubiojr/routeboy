package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/engine"
	"code.rbel.co/rubiojr/fade/components/mapview/oms"
	"code.rbel.co/rubiojr/fade/icons"
)

const (
	bookmarksKey = "bookmarks"
	// Leave one marker for the current search result and one for the point
	// of a track's elevation profile.
	bookmarksKept = 998
	bookmarkBytes = 1 << 20
)

type bookmark struct {
	Name     string           `json:"name"`
	Position mapview.Position `json:"position"`
}

func sameLocation(a, b mapview.Position) bool {
	return math.Abs(a.Latitude-b.Latitude) < 1e-7 && math.Abs(math.Remainder(a.Longitude-b.Longitude, 360)) < 1e-7
}

// updateBookmarkButton fills the star while the center is saved, when there
// is nothing to add.
func (s *screen) updateBookmarkButton() {
	icon := "star-new"
	enabled := !s.closed && s.addBookmark.OnTapped != nil && len(s.bookmarks) < bookmarksKept
	for _, saved := range s.bookmarks {
		if sameLocation(saved.Position, s.m.Center()) {
			icon, enabled = "starred", false
			break
		}
	}
	if s.addBookmark.Icon == nil || s.addBookmark.Icon.Name() != icon+"-symbolic.svg" {
		s.addBookmark.SetIcon(icons.Raw(icon))
	}
	if enabled && s.addBookmark.Disabled() {
		s.addBookmark.Enable()
	} else if !enabled && !s.addBookmark.Disabled() {
		s.addBookmark.Disable()
	}
}

// markPlaces keeps saved markers on the map while search results change.
func (s *screen) markPlaces(saved []bookmark, selected *oms.Place) error {
	markers := make([]mapview.Marker, 0, len(saved)+2)
	for _, b := range saved {
		markers = append(markers, mapview.Marker{Latitude: b.Position.Latitude, Longitude: b.Position.Longitude, Label: b.Name, Shape: mapview.Pin})
		if selected != nil && sameLocation(b.Position, mapview.Position{Latitude: selected.Latitude, Longitude: selected.Longitude}) {
			selected = nil // the saved marker already marks this place
		}
	}
	if selected != nil {
		markers = append(markers, mapview.Marker{Latitude: selected.Latitude, Longitude: selected.Longitude, Label: selected.Name})
	}
	if s.cursor != nil {
		markers = append(markers, *s.cursor)
	}
	return s.m.SetMarkers(markers...)
}

// setCursor marks a point of a track, or clears the mark with nil.
func (s *screen) setCursor(at *mapview.Marker) {
	s.cursor = at
	if err := s.markPlaces(s.bookmarks, s.place); err != nil {
		s.cursor = nil
	}
}

func (s *screen) setBookmarks(saved []bookmark) error {
	if len(saved) > bookmarksKept {
		return errors.New("at most 998 bookmarks can be saved")
	}
	for _, b := range saved {
		if math.IsNaN(b.Position.Zoom) || b.Position.Zoom < engine.MinZoom || b.Position.Zoom > engine.MaxZoom {
			return errors.New("a bookmark has an invalid zoom")
		}
	}
	if err := s.markPlaces(saved, s.place); err != nil {
		return err
	}
	s.bookmarks = slices.Clone(saved)
	s.updateBookmarkButton()
	return nil
}

func (p *pages) addBookmark() {
	s := p.screen
	if s.closed || s.addBookmark.Disabled() {
		return
	}
	at := s.m.Center()
	name := fmt.Sprintf("%.5f, %.5f", at.Latitude, at.Longitude)
	if s.place != nil && sameLocation(at, mapview.Position{Latitude: s.place.Latitude, Longitude: s.place.Longitude}) {
		name = short(s.place.Name, nameLength)
	}
	saved := append([]bookmark{{Name: name, Position: at}}, s.bookmarks...)
	p.saveBookmarks(saved)
}

func (p *pages) showBookmark(b bookmark) {
	p.stack.Select(mapPage)
	p.screen.visitAt(oms.Place{Name: b.Name, Latitude: b.Position.Latitude, Longitude: b.Position.Longitude}, b.Position)
}

func (p *pages) removeBookmark(b bookmark) {
	saved := slices.DeleteFunc(slices.Clone(p.screen.bookmarks), func(other bookmark) bool {
		return sameLocation(other.Position, b.Position)
	})
	selected := p.screen.place
	if selected != nil && sameLocation(b.Position, mapview.Position{Latitude: selected.Latitude, Longitude: selected.Longitude}) {
		p.screen.place = nil
	}
	if !p.saveBookmarks(saved) {
		p.screen.place = selected
	}
	p.screen.moved()
}

func (p *pages) loadBookmarks() error {
	raw := p.prefs.StringWithFallback(bookmarksKey, "[]")
	if len(raw) > bookmarkBytes {
		return errors.New("saved bookmarks exceed 1 MiB")
	}
	var saved []bookmark
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return fmt.Errorf("read saved bookmarks: %w", err)
	}
	return p.screen.setBookmarks(saved)
}

func (p *pages) saveBookmarks(saved []bookmark) bool {
	data, err := json.Marshal(saved)
	if err == nil && len(data) > bookmarkBytes {
		err = errors.New("saved bookmarks exceed 1 MiB")
	}
	if err == nil {
		err = p.screen.setBookmarks(saved)
	}
	if err != nil {
		p.bookmarkProblem = err.Error()
		p.stack.Select(bookmarksPage)
	} else {
		p.prefs.SetString(bookmarksKey, string(data))
		p.bookmarkProblem = ""
	}
	p.listBookmarks()
	return err == nil
}

func (p *pages) listBookmarks() {
	for _, row := range p.bookmarkList.Rows() {
		p.bookmarkList.Remove(row)
	}
	if p.bookmarkProblem != "" {
		p.bookmarkList.Add(boxedlist.NewActionRow("Bookmarks could not be saved or loaded", p.bookmarkProblem))
	}
	if len(p.screen.bookmarks) == 0 {
		p.bookmarkList.Add(boxedlist.NewActionRow("No bookmarks yet", "On the map, press Add bookmark to save its center."))
	}
	for _, b := range p.screen.bookmarks {
		row := boxedlist.NewActionRow(b.Name, fmt.Sprintf("%.5f, %.5f · zoom %g", b.Position.Latitude, b.Position.Longitude, b.Position.Zoom))
		row.SetIcon(icons.Raw("starred"))
		row.SetActivatable(func() { p.showBookmark(b) })
		remove := button.New("Remove", func() { p.removeBookmark(b) })
		remove.Style = button.Flat
		row.AddSuffix(remove)
		p.bookmarkList.Add(row)
	}
	p.saved.SetBadge(len(p.screen.bookmarks))
}
