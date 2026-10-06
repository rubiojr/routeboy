package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/engine"
	"code.rbel.co/rubiojr/fade/components/mapview/oms"
	"code.rbel.co/rubiojr/fade/icons"
)

const (
	// edge is the space between the window and what lies over the map.
	edge float32 = 12
	// panelWidth is the most a panel takes of a wide window.
	panelWidth float32 = 315
	// searchTime bounds one search.
	searchTime = 8 * time.Second
	placeLimit = 6
	// nameLength keeps the header short: a window cannot be narrower than
	// its header's text.
	nameLength = 32
	// fitZoom is the closest zoom that fitting a track goes to.
	fitZoom = 17
	// tileSize is the width of the world at zoom 0, in logical units.
	tileSize = 256
)

// finder searches places; an oms.Client is one.
type finder interface {
	Search(ctx context.Context, text string, near *oms.Near, limit int) ([]oms.Place, error)
}

// screen is a map with a search box, results, controls and a notice over it.
// It runs on the Fyne goroutine.
type screen struct {
	widget.BaseWidget

	m        *mapview.Map
	places   finder
	describe func(string)
	// visited, when set, receives every place visited.
	visited func(oms.Place)
	// do hands a function to the Fyne goroutine.
	do func(func())

	controls     *mapview.Controls
	notice       *mapview.Notice
	search       *mapview.SearchBox
	results      *boxedlist.List
	resultsPanel *mapview.Panel
	addBookmark  *button.Button
	locator      *locator
	crosshair    *fyne.Container
	bookmarks    []bookmark
	// place is the place visited, which the marker shows.
	place *oms.Place
	// cursor, when set, marks a point of a track on top of the other markers.
	cursor *mapview.Marker

	// inset is the room at the bottom kept for a bar over the screen: the
	// map ends above it. covered, when set, is the height that bar covers
	// now, which changes while it slides in or out; the controls stay above it.
	inset   float32
	covered func() float32
	r       *renderer

	// generation numbers the searches, so the result of an earlier one is
	// recognised and dropped.
	generation uint64
	cancel     context.CancelFunc
	workers    sync.WaitGroup
	closed     bool
}

// newScreen lays the controls over a map. describe receives a short text
// about what the map shows, for the window's header.
func newScreen(describe func(string), places finder, m *mapview.Map, home mapview.Position) *screen {
	s := &screen{m: m, places: places, describe: describe, do: fyne.Do}
	s.controls = mapview.NewControls(m)
	s.controls.SetHome(home)
	s.notice = mapview.NewNotice(m)
	s.addBookmark = button.NewIcon(icons.Raw("star-new"), nil)
	s.locator = newLocator(s)
	s.controls.Add(s.addBookmark, s.locator.button)
	s.crosshair = newCrosshair()

	s.search = mapview.NewSearchBox("Search places")
	s.search.OnSearch = s.find
	s.search.OnCleared = s.clear
	s.results = boxedlist.New()
	s.resultsPanel = mapview.NewPanel(container.NewVScroll(s.results))
	s.resultsPanel.Padding = 0 // the list is a card itself
	s.resultsPanel.Hide()

	m.OnMoved = func(mapview.Position) { s.moved() }
	// A map the device cannot draw says so in its middle.
	m.OnStatus = func(status mapview.Status) {
		if status.State == mapview.Unavailable {
			s.crosshair.Hide()
		} else {
			s.crosshair.Show()
		}
	}
	s.ExtendBaseWidget(s)
	s.moved()
	return s
}

// moved follows the map with the header.
func (s *screen) moved() {
	s.describe(s.title())
	s.updateBookmarkButton()
}

// title names the place visited while the map shows it, else the position.
func (s *screen) title() string {
	if s.place != nil {
		at, size := s.m.Locate(s.place.Latitude, s.place.Longitude), s.m.Size()
		if at.X >= 0 && at.Y >= 0 && at.X <= size.Width && at.Y <= size.Height {
			return short(s.place.Name, nameLength)
		}
	}
	at := s.m.Center()
	north, east := "N", "E"
	if at.Latitude < 0 {
		north = "S"
	}
	if at.Longitude < 0 {
		east = "W"
	}
	zoom := strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", at.Zoom), "0"), ".")
	return fmt.Sprintf("%.4f° %s, %.4f° %s · zoom %s", math.Abs(at.Latitude), north, math.Abs(at.Longitude), east, zoom)
}

// short cuts a text to a number of characters.
func short(text string, most int) string {
	if characters := []rune(text); len(characters) > most {
		return strings.TrimSpace(string(characters[:most-1])) + "…"
	}
	return text
}

// clear drops the results and any search under way.
func (s *screen) clear() {
	s.generation++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.resultsPanel.Hide()
	s.Refresh()
}

// find searches near the centre of the map, off the Fyne goroutine.
func (s *screen) find(text string) {
	if s.closed {
		return
	}
	s.clear()
	ctx, cancel := context.WithTimeout(context.Background(), searchTime)
	s.cancel = cancel
	generation := s.generation
	at := s.m.Center()
	s.workers.Go(func() {
		defer cancel()
		places, err := s.places.Search(ctx, text, &oms.Near{Latitude: at.Latitude, Longitude: at.Longitude}, placeLimit)
		s.do(func() {
			if s.closed || generation != s.generation {
				return
			}
			s.cancel = nil
			s.show(places, err)
		})
	})
}

// show lists the places found, or says why there are none.
func (s *screen) show(places []oms.Place, err error) {
	for _, row := range s.results.Rows() {
		s.results.Remove(row)
	}
	switch {
	case errors.Is(err, oms.ErrQuery):
		s.results.Add(boxedlist.NewActionRow("Use one to eight words", ""))
	case err != nil:
		s.results.Add(boxedlist.NewActionRow("The search did not work", err.Error()))
	case len(places) == 0:
		s.results.Add(boxedlist.NewActionRow("No places found", ""))
	}
	for _, place := range places {
		row := boxedlist.NewActionRow(place.Name, detail(place))
		row.SetActivatable(func() { s.visit(place) })
		s.results.Add(row)
	}
	s.resultsPanel.Show()
	s.Refresh()
}

// detail says what a place is, where, and how far away.
func detail(place oms.Place) string {
	parts := []string{strings.ReplaceAll(place.Type, "_", " ")}
	if place.Address != "" {
		parts = append(parts, place.Address)
	}
	switch {
	case place.Distance >= 1000:
		parts = append(parts, fmt.Sprintf("%.0f km", place.Distance/1000))
	case place.Distance > 0:
		parts = append(parts, fmt.Sprintf("%.0f m", place.Distance))
	}
	return strings.Join(parts, " · ")
}

// visit shows a place and marks it: a settlement from further away than a
// shop or a station.
func (s *screen) visit(place oms.Place) {
	zoom := 16.0
	if place.Category == "place" {
		zoom = 13
	}
	s.visitAt(place, mapview.Position{Latitude: place.Latitude, Longitude: place.Longitude, Zoom: zoom})
}

func (s *screen) visitAt(place oms.Place, position mapview.Position) {
	if err := s.markPlaces(s.bookmarks, &place); err != nil {
		s.show(nil, err)
		return
	}
	s.search.Cancel()
	s.clear()
	s.place = &place
	s.m.MoveTo(position)
	s.moved() // the map may have been there already
	if s.visited != nil {
		s.visited(place)
	}
}

// setInset keeps room at the bottom of the screen for a bar: the map ends
// above it. Only the bottom edge of the map moves.
func (s *screen) setInset(height float32) {
	if height == s.inset {
		return
	}
	s.inset = height
	s.relayout()
}

// relayout places what lies over the map again, as a bar over it moves.
func (s *screen) relayout() {
	if s.r != nil {
		s.r.Layout(s.Size())
	}
}

// fit shows the area between two corners whole, in the part of the map that
// what lies over it leaves clear, as close as fitZoom: below the search box,
// left of the controls and above the attribution.
func (s *screen) fit(south, west, north, east float64) {
	size := s.m.Size()
	top := edge + s.search.MinSize().Height + edge
	right := edge + s.controls.MinSize().Width + edge
	bottom := max(edge, s.m.CreditHeight()) + edge
	left := edge
	width, height := float64(size.Width-left-right), float64(size.Height-top-bottom)
	if width < 1 || height < 1 {
		width, height = float64(size.Width), float64(size.Height)
		top, right, bottom, left = 0, 0, 0, 0
	}
	x1, y1 := mercator(north, west)
	x2, y2 := mercator(south, east)
	zoom := float64(fitZoom)
	if span := max((x2-x1)*tileSize/width, (y2-y1)*tileSize/height); span > 0 {
		zoom = min(zoom, math.Log2(1/span))
	}
	zoom = math.Floor(max(engine.MinZoom, min(engine.MaxZoom, zoom))/engine.ZoomStep) * engine.ZoomStep
	// The middle of the clear part is off the middle of the map when more
	// lies over one side than over the other.
	world := tileSize * math.Exp2(zoom)
	dx, dy := float64(left-right)/2/world, float64(top-bottom)/2/world
	latitude, longitude := geographic((x1+x2)/2-dx, (y1+y2)/2-dy)
	s.m.MoveTo(mapview.Position{Latitude: latitude, Longitude: longitude, Zoom: zoom})
	s.moved()
}

// mercator projects a place on a world one unit wide.
func mercator(latitude, longitude float64) (x, y float64) {
	latitude = max(-85.05112878, min(85.05112878, latitude)) * math.Pi / 180
	return (longitude + 180) / 360, (1 - math.Log(math.Tan(latitude)+1/math.Cos(latitude))/math.Pi) / 2
}

// geographic is the place at a point of a world one unit wide.
func geographic(x, y float64) (latitude, longitude float64) {
	return math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi, x*360 - 180
}

// close stops the map and the searches. Call it when the window closes.
func (s *screen) close() {
	s.closed = true
	s.locator.stop()
	s.updateBookmarkButton()
	s.search.Cancel()
	s.clear()
	s.m.Close()
}

func (s *screen) CreateRenderer() fyne.WidgetRenderer {
	r := &renderer{s: s}
	s.r = r
	r.Refresh()
	return r
}

type renderer struct {
	s *screen
	// inset is the one the map was last laid out with.
	inset float32
}

func (r *renderer) MinSize() fyne.Size { return fyne.NewSize(280, 320) }

func (r *renderer) Layout(size fyne.Size) {
	s := r.s
	area := fyne.NewSize(size.Width, max(0, size.Height-s.inset))
	before := s.m.Size()
	s.m.Resize(area)
	if s.inset != r.inset && before == fyne.NewSize(size.Width, max(0, size.Height-r.inset)) {
		// A map keeps its centre as it changes size. When only the inset
		// changed, keep the top edge still instead: the bar covers the map.
		s.m.PanBy(0, float64(s.inset-r.inset)/2)
	}
	r.inset = s.inset
	s.crosshair.Move(fyne.NewPos((area.Width-crosshairSize)/2, (area.Height-crosshairSize)/2))

	width := min(panelWidth, max(0, size.Width-2*edge))
	top := edge
	height := s.search.MinSize().Height
	s.search.Move(fyne.NewPos(edge, top))
	s.search.Resize(fyne.NewSize(width, height))
	top += height + edge

	if s.resultsPanel.Visible() {
		height = min(s.results.HeightForWidth(width), max(60, size.Height-top-edge))
		s.resultsPanel.Move(fyne.NewPos(edge, top))
		s.resultsPanel.Resize(fyne.NewSize(width, height))
	}

	// What lies at the bottom stays clear of the attribution, and of a bar
	// sliding over the map.
	bottom := area.Height - max(edge, s.m.CreditHeight())
	if s.covered != nil {
		if bar := s.covered(); bar > s.inset {
			bottom = min(bottom, size.Height-bar-edge)
		}
	}
	buttons := s.controls.MinSize()
	s.controls.Resize(buttons)
	s.controls.Move(fyne.NewPos(size.Width-buttons.Width-edge, bottom-buttons.Height))

	if s.notice.Visible() {
		// A notice is as tall as its text needs at the width it gets.
		room := max(0, min(panelWidth, size.Width-3*edge-buttons.Width))
		s.notice.Resize(fyne.NewSize(room, s.notice.MinSize().Height))
		height = s.notice.MinSize().Height
		s.notice.Resize(fyne.NewSize(room, height))
		s.notice.Move(fyne.NewPos(edge, bottom-height))
	}
}

func (r *renderer) Refresh() {
	s := r.s
	r.Layout(s.Size())
	for _, o := range r.Objects() {
		o.Refresh()
	}
}

func (r *renderer) Objects() []fyne.CanvasObject {
	s := r.s
	return []fyne.CanvasObject{s.m, s.crosshair, s.controls, s.notice, s.search, s.resultsPanel}
}

func (*renderer) Destroy() {}
