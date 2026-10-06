package main

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/store"
	"code.rbel.co/rubiojr/fade/components/viewswitcher"
)

func TestBookmarkButtonSavesTheCenterWithoutMovingIt(t *testing.T) {
	p, _, w := opened(t, 420, store.NewMemory(1<<20))
	s := p.screen
	if p.saved.Title != "Bookmarks" || p.bookmarkList.Rows()[0].(*boxedlist.ActionRow).Title != "No bookmarks yet" {
		t.Fatal("the switcher has no empty Bookmarks page")
	}
	s.m.PanBy(30, -70)
	s.m.ZoomBy(0.5)
	if s.addBookmark.Disabled() || !drawnInFull(s.addBookmark.Icon) {
		t.Fatal("the button to add a bookmark looks disabled")
	}
	before := s.m.Center()
	mapTaps := 0
	s.m.OnTapped = func(mapview.Coordinate) { mapTaps++ }
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(s.addBookmark)
	test.TapCanvas(w.Canvas(), at.AddXY(s.addBookmark.Size().Width/2, s.addBookmark.Size().Height/2))
	if len(s.bookmarks) != 1 || s.bookmarks[0].Position != before || s.m.Center() != before || mapTaps != 0 {
		t.Fatalf("bookmarks %+v, center %+v, map taps %d", s.bookmarks, s.m.Center(), mapTaps)
	}
	if !s.addBookmark.Disabled() || s.addBookmark.Icon.Name() != "starred-symbolic.svg" || p.saved.Badge != 1 {
		t.Fatal("the saved bookmark was not acknowledged")
	}
	test.Tap(s.addBookmark)
	if len(s.bookmarks) != 1 {
		t.Fatal("a repeated press made a duplicate bookmark")
	}
	s.m.PanBy(100, 0)
	if s.addBookmark.Disabled() || !drawnInFull(s.addBookmark.Icon) {
		t.Fatal("moving to another place did not re-enable adding a bookmark")
	}
	test.Tap(s.addBookmark)
	if len(s.bookmarks) != 2 || p.saved.Badge != 2 {
		t.Fatal("the second bookmark replaced the first")
	}
	var stored []bookmark
	if err := json.Unmarshal([]byte(p.prefs.String(bookmarksKey)), &stored); err != nil || !reflect.DeepEqual(stored, s.bookmarks) {
		t.Fatalf("stored %+v, error %v, want %+v", stored, err, s.bookmarks)
	}
}

// drawnInFull reports whether an icon has no translucent parts, which would
// make an enabled button look disabled.
func drawnInFull(icon fyne.Resource) bool {
	return icon != nil && !strings.Contains(string(icon.Content()), "opacity")
}

func TestCrosshairTargetsTheBookmarkAndPassesInputToTheMap(t *testing.T) {
	p, _, w := opened(t, 420, store.NewMemory(1<<20))
	s := p.screen
	for _, width := range []float32{420, 900} {
		w.Resize(fyne.NewSize(width, 800))
		center := fyne.NewPos(s.m.Size().Width/2, s.m.Size().Height/2)
		if target := s.crosshair.Position().AddXY(crosshairSize/2, crosshairSize/2); target != center {
			t.Fatalf("crosshair at %v, want map center %v", target, center)
		}
		absolute := fyne.CurrentApp().Driver().AbsolutePositionForObject(s.m).Add(center)
		arm := absolute.AddXY(6, 0)
		mapTaps := 0
		s.m.OnTapped = func(mapview.Coordinate) { mapTaps++ }
		test.TapCanvas(w.Canvas(), arm)
		if mapTaps != 1 {
			t.Fatal("the crosshair blocked a map tap")
		}
		before := s.m.Center()
		test.Drag(w.Canvas(), arm, 25, -15)
		if s.m.Center() == before {
			t.Fatal("the crosshair blocked a drag")
		}
		zoom := s.m.Center().Zoom
		test.Scroll(w.Canvas(), arm, 0, 10)
		if s.m.Center().Zoom <= zoom {
			t.Fatal("the crosshair blocked scroll zoom")
		}
		target := s.m.CoordinateAt(center)
		test.Tap(s.addBookmark)
		saved := s.bookmarks[0].Position
		if math.Abs(saved.Latitude-target.Latitude) > 1e-9 || math.Abs(saved.Longitude-target.Longitude) > 1e-9 {
			t.Fatalf("bookmark at %+v, want crosshair target %+v", saved, target)
		}
	}
}

func TestBookmarkSwitcherReturnsToSavedPlaceAndZoom(t *testing.T) {
	p, _, _ := opened(t, 420, store.NewMemory(1<<20))
	s := p.screen
	s.visit(prado)
	s.m.ZoomBy(-1.5)
	test.Tap(s.addBookmark)
	saved := s.bookmarks[0]
	if saved.Name != prado.Name {
		t.Errorf("bookmark name %q, want the selected place's name", saved.Name)
	}
	s.visit(retiro)
	if len(s.bookmarks) != 1 {
		t.Fatal("a search result changed saved bookmarks")
	}
	s.m.MoveTo(madrid)
	marker := -1
	s.m.OnMarkerTapped = func(index int) { marker = index }
	test.TapAt(s.m, s.m.Locate(prado.Latitude, prado.Longitude))
	if marker != 0 {
		t.Fatal("the saved marker disappeared after a search")
	}
	marker = -1
	test.TapAt(s.m, s.m.Locate(retiro.Latitude, retiro.Longitude))
	if marker != 1 {
		t.Fatal("the selected search result was not marked alongside the bookmark")
	}

	switcher := viewswitcher.New(p.stack, viewswitcher.Wide)
	switcher.Resize(fyne.NewSize(switcher.NaturalWidth(), 34))
	test.TapAt(switcher, fyne.NewPos(switcher.Size().Width*5/8, 17))
	if p.stack.Selected() != bookmarksPage || p.details.sheet.Visible() || !p.saved.Content.Visible() {
		t.Fatal("the switcher did not open Bookmarks")
	}
	test.Tap(p.bookmarkList.Rows()[0].(*boxedlist.ActionRow))
	if p.stack.Selected() != mapPage || !p.details.sheet.Visible() {
		t.Fatal("choosing a bookmark did not return to the map")
	}
	got := s.m.Center()
	if math.Abs(got.Latitude-saved.Position.Latitude) > 1e-9 || math.Abs(got.Longitude-saved.Position.Longitude) > 1e-9 || got.Zoom != saved.Position.Zoom {
		t.Fatalf("center %+v, want saved position %+v", got, saved.Position)
	}
	if !s.addBookmark.Disabled() {
		t.Fatal("a bookmark could be duplicated after returning to it")
	}
}

func TestRemovingABookmarkRemovesItsMarkerAndStoredData(t *testing.T) {
	p, _, _ := opened(t, 420, store.NewMemory(1<<20))
	s := p.screen
	s.visit(prado)
	test.Tap(s.addBookmark)
	p.showBookmark(s.bookmarks[0])
	p.stack.Select(bookmarksPage)
	row := p.bookmarkList.Rows()[0].(*boxedlist.ActionRow)
	test.Tap(row.Suffixes()[0].(*button.Button))
	if len(s.bookmarks) != 0 || p.saved.Badge != 0 || p.stack.Selected() != bookmarksPage {
		t.Fatal("Remove kept the bookmark or activated its row")
	}
	if row := p.bookmarkList.Rows()[0].(*boxedlist.ActionRow); row.Title != "No bookmarks yet" {
		t.Fatalf("empty list shows %q", row.Title)
	}
	var stored []bookmark
	if err := json.Unmarshal([]byte(p.prefs.String(bookmarksKey)), &stored); err != nil || len(stored) != 0 {
		t.Fatalf("stored bookmarks %+v, error %v", stored, err)
	}
	p.stack.Select(mapPage)
	markerTaps := 0
	s.m.OnMarkerTapped = func(int) { markerTaps++ }
	test.TapAt(s.m, s.m.Locate(prado.Latitude, prado.Longitude))
	if markerTaps != 0 || s.addBookmark.Disabled() {
		t.Fatal("removed marker still responds or cannot be added again")
	}
}

func TestBookmarksReloadFromPreferences(t *testing.T) {
	p, _, _ := opened(t, 420, store.NewMemory(1<<20))
	p.screen.visit(prado)
	test.Tap(p.screen.addBookmark)
	p.screen.visit(retiro)
	test.Tap(p.screen.addBookmark)
	s := newScreen(func(string) {}, &index{}, mapview.New(waiting{}, mapview.Options{Start: madrid}), madrid)
	t.Cleanup(s.close)
	reopened := newPages(s, store.NewMemory(1<<20), settings{}, p.prefs)
	if !reflect.DeepEqual(s.bookmarks, p.screen.bookmarks) || reopened.saved.Badge != 2 || len(reopened.bookmarkList.Rows()) != 2 {
		t.Fatalf("restored bookmarks %+v, badge %d", s.bookmarks, reopened.saved.Badge)
	}
	s.m.Resize(fyne.NewSize(400, 400))
	marker := -1
	s.m.OnMarkerTapped = func(index int) { marker = index }
	test.TapAt(s.m, s.m.Locate(retiro.Latitude, retiro.Longitude))
	if marker != 0 {
		t.Fatal("restored bookmark did not appear on the map")
	}
}

func TestInvalidBookmarksLeaveExistingDataAlone(t *testing.T) {
	p, _, _ := opened(t, 420, store.NewMemory(1<<20))
	test.Tap(p.screen.addBookmark)
	want := p.screen.bookmarks[0]
	if err := p.screen.setBookmarks(make([]bookmark, bookmarksKept+1)); err == nil {
		t.Fatal("too many bookmarks were accepted")
	}
	for name, raw := range map[string]string{
		"malformed":   `{`,
		"coordinates": `[{"name":"bad","position":{"Latitude":100,"Zoom":12}}]`,
		"zoom":        `[{"name":"bad","position":{"Zoom":50}}]`,
		"too large":   strings.Repeat(" ", bookmarkBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			p.prefs.SetString(bookmarksKey, raw)
			if err := p.loadBookmarks(); err == nil {
				t.Fatal("invalid bookmarks were loaded")
			}
			if len(p.screen.bookmarks) != 1 || p.screen.bookmarks[0] != want || p.prefs.String(bookmarksKey) != raw {
				t.Fatal("failed load changed the bookmarks or preferences")
			}
		})
	}
	if p.saveBookmarks([]bookmark{{Position: mapview.Position{Latitude: math.NaN()}}}) || p.bookmarkProblem == "" {
		t.Fatal("a failed save was not reported")
	}
	if len(p.screen.bookmarks) != 1 || p.screen.bookmarks[0] != want {
		t.Fatal("a failed save changed the bookmarks")
	}
}
