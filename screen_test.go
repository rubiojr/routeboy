package main

import (
	"context"
	"errors"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/oms"
	"code.rbel.co/rubiojr/fade/components/mapview/source"
	"code.rbel.co/rubiojr/fade/style"
)

var madrid = mapview.Position{Latitude: 40.4168, Longitude: -3.7038, Zoom: 12}

// waiting is a source that never opens, so its map stays as the test leaves it.
type waiting struct{}

func (waiting) Open(ctx context.Context) (source.Info, error) {
	<-ctx.Done()
	return source.Info{}, ctx.Err()
}
func (waiting) Tile(context.Context, uint32, uint32, uint32) ([]byte, error) { return nil, nil }
func (waiting) Glyphs(context.Context, string, int) ([]byte, error)          { return nil, nil }

// index answers searches and records them.
type index struct {
	mu     sync.Mutex
	places []oms.Place
	err    error
	asked  []string
	near   []oms.Near
	// hold, when set, keeps a search waiting until it is closed or the
	// search is cancelled.
	hold chan struct{}
}

func (i *index) Search(ctx context.Context, text string, near *oms.Near, limit int) ([]oms.Place, error) {
	i.mu.Lock()
	i.asked, i.near = append(i.asked, text), append(i.near, *near)
	hold := i.hold
	i.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return i.places[:min(limit, len(i.places))], i.err
}

// queue stands in for the Fyne goroutine: results wait in it until the test
// takes them.
type queue struct {
	mu      sync.Mutex
	pending []func()
}

func (q *queue) do(f func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, f)
}

// searched waits for the searches of a screen and hands over their results.
func searched(s *screen) {
	s.workers.Wait()
	q := results[s]
	q.mu.Lock()
	pending := q.pending
	q.pending = nil
	q.mu.Unlock()
	for _, f := range pending {
		f()
	}
}

// results holds the queue of every screen under test.
var results = map[*screen]*queue{}

// shown puts a screen in a window of 400x700 units and records its titles.
func shown(t *testing.T, places finder) (*screen, *[]string) {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(style.NewTheme(style.WithScheme(style.SchemeLight), style.WithAccent(style.AccentBlue)))
	var titles []string
	m := mapview.New(waiting{}, mapview.Options{Start: madrid})
	s := newScreen(func(title string) { titles = append(titles, title) }, places, m, madrid)
	q := &queue{}
	s.do, results[s] = q.do, q
	t.Cleanup(func() {
		s.close()
		searched(s)
		delete(results, s)
	})
	window := test.NewTempWindow(t, s)
	window.SetPadded(false)
	window.Resize(fyne.NewSize(400, 700))
	return s, &titles
}

// rows returns the titles and subtitles of the results.
func rows(s *screen) (titles, details []string) {
	for _, row := range s.results.Rows() {
		action := row.(*boxedlist.ActionRow)
		titles, details = append(titles, action.Title), append(details, action.Subtitle)
	}
	return titles, details
}

func last(titles *[]string) string { return (*titles)[len(*titles)-1] }

var prado = oms.Place{ID: 1, Kind: "way", Name: "Museo Nacional del Prado", Category: "tourism", Type: "museum",
	Address: "Paseo del Prado", Latitude: 40.4138, Longitude: -3.6921, Distance: 1100}

func TestSearchListsPlacesNearTheMap(t *testing.T) {
	found := &index{places: []oms.Place{prado,
		{Name: "Prado del Rey", Category: "place", Type: "village", Latitude: 36.79, Longitude: -5.55, Distance: 431000},
		{Name: "Prado", Category: "highway", Type: "bus_stop", Latitude: 40.41, Longitude: -3.69, Distance: 420}}}
	s, _ := shown(t, found)
	if s.resultsPanel.Visible() {
		t.Fatal("results show before a search")
	}

	s.search.Entry.SetText("prado")
	s.search.Entry.Field().TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn}) // searches without the pause
	searched(s)

	if len(found.asked) != 1 || found.asked[0] != "prado" {
		t.Fatalf("searches %q, want one for prado", found.asked)
	}
	if near := found.near[0]; math.Abs(near.Latitude-madrid.Latitude) > 1e-9 || math.Abs(near.Longitude-madrid.Longitude) > 1e-9 {
		t.Errorf("searched near %+v, want the centre of the map", near)
	}
	titles, details := rows(s)
	if !s.resultsPanel.Visible() || strings.Join(titles, "|") != "Museo Nacional del Prado|Prado del Rey|Prado" {
		t.Fatalf("results visible %v, rows %q", s.resultsPanel.Visible(), titles)
	}
	if want := "museum · Paseo del Prado · 1 km|village · 431 km|bus stop · 420 m"; strings.Join(details, "|") != want {
		t.Errorf("details %q, want %q", strings.Join(details, "|"), want)
	}

	s.search.Entry.SetText("  ")
	if s.resultsPanel.Visible() {
		t.Error("results stay after the search text was cleared")
	}
}

func TestSearchSaysWhyThereAreNoPlaces(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		title, detail string
	}{
		{"nothing found", nil, "No places found", ""},
		{"unusable text", oms.ErrQuery, "Use one to eight words", ""},
		{"server busy", oms.ErrBusy, "The search did not work", "oms: server busy"},
	}
	for _, tt := range tests {
		s, _ := shown(t, &index{err: tt.err})
		s.find("anything")
		searched(s)
		titles, details := rows(s)
		if !s.resultsPanel.Visible() || len(titles) != 1 || titles[0] != tt.title || details[0] != tt.detail {
			t.Errorf("%s: visible %v, rows %q, details %q", tt.name, s.resultsPanel.Visible(), titles, details)
		}
	}
}

func TestLaterSearchReplacesAnEarlierOne(t *testing.T) {
	found := &index{places: []oms.Place{prado}, hold: make(chan struct{})}
	s, _ := shown(t, found)
	s.find("first")
	s.find("second") // cancels the first, which was still waiting
	close(found.hold)
	searched(s)

	if len(found.asked) != 2 {
		t.Fatalf("searches %q, want two", found.asked)
	}
	if titles, _ := rows(s); len(titles) != 1 {
		t.Errorf("rows %q, want the places of one search", titles)
	}
}

func TestVisitMovesTheMapAndMarksThePlace(t *testing.T) {
	s, titles := shown(t, &index{places: []oms.Place{prado}})
	if want := "40.4168° N, 3.7038° W · zoom 12"; last(titles) != want {
		t.Fatalf("title %q, want %q", last(titles), want)
	}
	markerTaps := 0
	s.m.OnMarkerTapped = func(int) { markerTaps++ }
	test.TapAt(s.m, fyne.NewPos(200, 350))
	if markerTaps != 0 {
		t.Fatal("a marker responds before a place was visited")
	}
	s.find("prado")
	searched(s)

	test.Tap(s.results.Rows()[0].(*boxedlist.ActionRow))

	at := s.m.Center()
	if math.Abs(at.Latitude-prado.Latitude) > 1e-9 || math.Abs(at.Longitude-prado.Longitude) > 1e-9 || at.Zoom != 16 {
		t.Errorf("map at %+v, want the place at zoom 16", at)
	}
	if s.resultsPanel.Visible() {
		t.Error("results stay after a place was chosen")
	}
	if last(titles) != "Museo Nacional del Prado" {
		t.Errorf("title %q, want the name of the place", last(titles))
	}
	centre := fyne.NewPos(200, 350)
	test.TapAt(s.m, centre)
	if markerTaps != 1 {
		t.Fatal("the place's marker did not respond at the centre")
	}

	s.m.PanBy(120, -60)
	test.TapAt(s.m, centre.AddXY(120, -60))
	if markerTaps != 2 {
		t.Fatal("the place's marker did not follow the map")
	}
	s.m.PanBy(400, 0)
	if !strings.Contains(last(titles), "° N") {
		t.Errorf("title %q, want the position once the place left the map", last(titles))
	}

	s.visit(oms.Place{Name: "Cercedilla", Category: "place", Type: "town", Latitude: 40.74, Longitude: -4.06})
	if zoom := s.m.Center().Zoom; zoom != 13 {
		t.Errorf("zoom %v for a town, want 13", zoom)
	}
}

func TestTitlesStayShort(t *testing.T) {
	s, titles := shown(t, &index{})
	s.visit(oms.Place{Name: strings.Repeat("Museo Nacional ", 8), Latitude: 40.4, Longitude: -3.7})
	if got := []rune(last(titles)); len(got) > nameLength || got[len(got)-1] != '…' {
		t.Errorf("title %q of %d characters, want at most %d and an ellipsis", string(got), len(got), nameLength)
	}
	s.m.MoveTo(mapview.Position{Latitude: -33.9249, Longitude: 18.4241, Zoom: 9.5})
	if want := "33.9249° S, 18.4241° E · zoom 9.5"; last(titles) != want {
		t.Errorf("title %q, want %q", last(titles), want)
	}
	if got := short("Madrid", nameLength); got != "Madrid" {
		t.Errorf("short name became %q", got)
	}
}

func TestClosedScreenSearchesNoMore(t *testing.T) {
	found := &index{places: []oms.Place{prado}, hold: make(chan struct{})}
	s, _ := shown(t, found)
	s.find("prado")
	s.close()
	searched(s) // the search was cancelled

	if s.resultsPanel.Visible() {
		t.Error("a closed screen showed results")
	}
	s.find("again")
	searched(s)
	if len(found.asked) != 1 {
		t.Errorf("searches %q after closing, want only the first", found.asked)
	}
	if state := s.m.Status().State; state != mapview.Starting {
		t.Errorf("map state %v", state)
	}
}

func TestLayoutKeepsControlsOnTheWindow(t *testing.T) {
	s, _ := shown(t, &index{places: []oms.Place{prado, prado, prado, prado, prado, prado}})
	s.find("prado")
	searched(s)
	for _, size := range []fyne.Size{{Width: 300, Height: 420}, {Width: 400, Height: 700}, {Width: 1200, Height: 800}} {
		s.Resize(size)
		for name, o := range map[string]fyne.CanvasObject{"search": s.search, "results": s.resultsPanel, "controls": s.controls, "notice": s.notice} {
			right, bottom := o.Position().X+o.Size().Width, o.Position().Y+o.Size().Height
			if o.Position().X < edge || o.Position().Y < edge || right > size.Width-edge || bottom > size.Height-edge {
				t.Errorf("%v: %s spans %v to (%v, %v)", size, name, o.Position(), right, bottom)
			}
		}
		if s.search.Size().Width > panelWidth {
			t.Errorf("%v: search box %v wide", size, s.search.Size().Width)
		}
		at := fyne.CurrentApp().Driver().AbsolutePositionForObject
		if star, zoom := at(s.addBookmark), at(s.controls.ZoomIn); star.X != zoom.X || star.Y >= zoom.Y {
			t.Errorf("%v: the bookmark button at %v, zoom in at %v", size, star, zoom)
		}
		if s.m.Size() != size {
			t.Errorf("%v: map of size %v", size, s.m.Size())
		}
	}
}

func TestParse(t *testing.T) {
	var usage strings.Builder
	output = &usage
	t.Cleanup(func() { output = os.Stderr })
	s, err := parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.home != madrid || s.scheme != style.SchemeSystem || s.megabytes != 256 || s.offline || s.fullscreen ||
		s.tracks != defaultTracks() || filepath.Base(s.tracks) != "Tracks" {
		t.Errorf("defaults %+v", s)
	}
	s, err = parse([]string{"-lat", "41.38", "-lon", "2.17", "-zoom", "14", "-scheme", "dark", "-offline", "-fullscreen", "-store-mib", "64", "-oms", "http://localhost:8080", "-tracks", "/srv/gpx"})
	if err != nil {
		t.Fatal(err)
	}
	if s.home != (mapview.Position{Latitude: 41.38, Longitude: 2.17, Zoom: 14}) || s.scheme != style.SchemeDark ||
		s.megabytes != 64 || !s.offline || !s.fullscreen || s.server != "http://localhost:8080" || s.tracks != "/srv/gpx" {
		t.Errorf("settings %+v", s)
	}
	s, err = parse([]string{"-tracks", "gpx"})
	if wd, _ := os.Getwd(); err != nil || s.tracks != filepath.Join(wd, "gpx") {
		t.Errorf("relative tracks folder %q, error %v", s.tracks, err)
	}
	for _, arguments := range [][]string{{"-profile", "watch"}, {"-scheme", "sepia"}, {"-store-mib", "0"}, {"-tracks", ""}, {"-unknown"}} {
		if _, err := parse(arguments); err == nil {
			t.Errorf("%q was accepted", arguments)
		}
	}
	usage.Reset()
	if _, err := parse([]string{"-h"}); !errors.Is(err, flag.ErrHelp) || !strings.Contains(usage.String(), "-store-mib") {
		t.Errorf("-h: error %v, usage %q", err, usage.String())
	}
}
