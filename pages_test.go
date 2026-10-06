package main

import (
	"math"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/oms"
	"code.rbel.co/rubiojr/fade/components/mapview/store"
	"code.rbel.co/rubiojr/fade/components/viewswitcher"
	"code.rbel.co/rubiojr/fade/components/window"
	"code.rbel.co/rubiojr/fade/style"
)

var retiro = oms.Place{ID: 2, Kind: "way", Name: "Parque del Retiro", Category: "leisure", Type: "park",
	Latitude: 40.4153, Longitude: -3.6845, Distance: 1700}

// opened puts the pages in a window of the given width, as main does, with
// an empty tracks folder.
func opened(t *testing.T, width float32, data store.Store) (*pages, *viewswitcher.View, *window.Window) {
	t.Helper()
	return openedWith(t, width, data, t.TempDir(), test.NewApp())
}

// openedWith puts the pages in a window, with tracks from a folder.
func openedWith(t *testing.T, width float32, data store.Store, tracks string, a fyne.App) (*pages, *viewswitcher.View, *window.Window) {
	t.Helper()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(style.NewTheme(style.WithScheme(style.SchemeLight), style.WithAccent(style.AccentBlue)))
	w := window.New(a, "Map", window.WithMode(window.Decorated))
	t.Cleanup(w.Close)
	s := newScreen(w.Header.SetSubtitle, &index{}, mapview.New(waiting{}, mapview.Options{Start: madrid}), madrid)
	t.Cleanup(s.close)
	p := newPages(s, data, settings{server: "https://oms.example", offline: true, tracks: tracks}, a.Preferences())
	view := p.mount(w)
	w.Resize(fyne.NewSize(width, 800))
	return p, view, w
}

// listed returns the titles of the Places page.
func listed(p *pages) string {
	var titles []string
	for _, row := range p.list.Rows() {
		titles = append(titles, row.(*boxedlist.ActionRow).Title)
	}
	return strings.Join(titles, "|")
}

func TestSwitcherMovesToTheBottomOfANarrowWindow(t *testing.T) {
	p, view, w := opened(t, 420, store.NewMemory(1<<20))
	if !view.Narrow() || w.Header.TitleWidget != nil {
		t.Fatalf("420 wide: narrow %v, title widget %v", view.Narrow(), w.Header.TitleWidget)
	}
	if got, want := p.screen.Size(), fyne.NewSize(420, view.Size().Height-46); got != want {
		t.Errorf("420 wide: map page of %v, want %v", got, want)
	}
	w.Resize(fyne.NewSize(900, 800))
	if view.Narrow() || w.Header.TitleWidget == nil {
		t.Fatalf("900 wide: narrow %v, title widget %v", view.Narrow(), w.Header.TitleWidget)
	}
	if got := p.screen.Size(); got != view.Size() || got.Width != 900 {
		t.Errorf("900 wide: map page of %v in a view of %v", got, view.Size())
	}
	if got := w.Content().MinSize().Width; got > viewswitcher.Breakpoint {
		t.Errorf("the window needs %v, so it cannot reach the breakpoint", got)
	}
}

func TestPlacesVisitedAreListedAndCounted(t *testing.T) {
	p, _, _ := opened(t, 420, store.NewMemory(1<<20))
	if listed(p) != "No places yet" || p.places.Badge != 0 {
		t.Fatalf("rows %q, badge %d before a visit", listed(p), p.places.Badge)
	}
	p.screen.visit(prado)
	p.screen.visit(retiro)
	p.screen.visit(prado) // known: it moves to the top and is not counted again
	if listed(p) != "Museo Nacional del Prado|Parque del Retiro" {
		t.Errorf("rows %q", listed(p))
	}
	if p.places.Badge != 2 || !p.places.NeedsAttention {
		t.Errorf("badge %d, needs attention %v", p.places.Badge, p.places.NeedsAttention)
	}

	p.stack.OnChanged(placesPage) // what a switcher does once the user chose the page
	if p.places.Badge != 0 || p.places.NeedsAttention {
		t.Errorf("badge %d, needs attention %v after the page was opened", p.places.Badge, p.places.NeedsAttention)
	}

	p.stack.Select(placesPage)
	p.screen.m.MoveTo(madrid)
	test.Tap(p.list.Rows()[1].(*boxedlist.ActionRow))
	if p.stack.Selected() != mapPage {
		t.Fatalf("page %d after a place was chosen, want the map", p.stack.Selected())
	}
	if at := p.screen.m.Center(); math.Abs(at.Latitude-retiro.Latitude) > 1e-9 || math.Abs(at.Longitude-retiro.Longitude) > 1e-9 {
		t.Errorf("map at %+v, want the place chosen", at)
	}
	if listed(p) != "Parque del Retiro|Museo Nacional del Prado" || p.places.Badge != 0 {
		t.Errorf("rows %q, badge %d", listed(p), p.places.Badge)
	}

	for i := range 2 * placesKept {
		p.screen.visit(oms.Place{Name: "Place", Latitude: float64(i), Longitude: 1})
	}
	if got := len(p.list.Rows()); got != placesKept {
		t.Errorf("%d rows, want %d", got, placesKept)
	}
}

// unsized is a store that cannot tell how much it holds.
type unsized struct{}

func (unsized) Get(string) ([]byte, bool) { return nil, false }
func (unsized) Put(string, []byte) error  { return nil }

func TestAboutTellsWhatIsKept(t *testing.T) {
	data := store.NewMemory(4 << 20)
	p, _, _ := opened(t, 420, data)
	if got := p.kept.Subtitle; got != "0.0 MiB" {
		t.Errorf("kept %q of an empty store", got)
	}
	if err := data.Put("tiles/1", make([]byte, 3<<19)); err != nil {
		t.Fatal(err)
	}
	p.showAbout()
	if got := p.kept.Subtitle; got != "1.5 MiB" {
		t.Errorf("kept %q, want 1.5 MiB", got)
	}
	p.showMain()
	p, _, _ = opened(t, 420, unsized{})
	if got := p.kept.Subtitle; got != "Not measured by this store" {
		t.Errorf("kept %q of a store without a size", got)
	}
}

func TestPrimaryMenuOpensFullPageAbout(t *testing.T) {
	p, view, w := opened(t, 320, store.NewMemory(1<<20))
	if pages := p.stack.Pages(); len(pages) != 4 || pages[mapPage].Title != "Map" || pages[tracksPage].Title != "Tracks" ||
		pages[bookmarksPage].Title != "Bookmarks" || pages[placesPage].Title != "Places" {
		t.Fatalf("unexpected top-level pages: %v", pages)
	}
	p.stack.Select(placesPage)
	// Tap the trailing header button through the canvas, as on a phone.
	at := fyne.NewPos(w.Header.Size().Width-24, w.Header.Size().Height/2)
	test.TapCanvas(w.Canvas(), at)
	focus := w.Canvas().Focused()
	if w.Canvas().Overlays().Top() == nil || focus == nil {
		t.Fatal("header menu did not open")
	}
	focus.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if !p.about.Visible() || view.Visible() || w.Canvas().Overlays().Top() != nil {
		t.Fatal("About did not replace the main views with a full page")
	}
	if !p.back.Visible() || p.menu.Visible() || w.Header.Title != "About Routeboy" || w.Canvas().Focused() != p.back {
		t.Fatal("About header did not offer focused back navigation")
	}
	for _, size := range []fyne.Size{fyne.NewSize(320, 568), fyne.NewSize(568, 360), fyne.NewSize(900, 800)} {
		w.Resize(size)
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.about)
		want := fyne.NewSize(size.Width, size.Height-w.Header.Size().Height)
		if p.about.Size() != want || pos != fyne.NewPos(0, w.Header.Size().Height) {
			t.Fatalf("About %v at %v, want full content area %v", p.about.Size(), pos, want)
		}
		if w.Header.TitleWidget != nil || w.Header.Subtitle != "" {
			t.Fatal("main view navigation leaked into About's header")
		}
	}
	backPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(p.back)
	test.TapCanvas(w.Canvas(), backPos.Add(fyne.NewPos(17, 17)))
	if p.about.Visible() || !view.Visible() || p.stack.Selected() != placesPage {
		t.Fatal("back did not return to Places")
	}
	if view.Narrow() || w.Header.TitleWidget == nil || w.Header.Title != "Map" || !p.menu.Visible() || p.back.Visible() {
		t.Fatal("back did not restore the main header at its new width")
	}
}

func TestAboutReturnsToMapAfterResizing(t *testing.T) {
	for _, tt := range []struct {
		name          string
		before, after float32
	}{
		{"desktop", 900, 900},
		{"phone", 320, 320},
		{"desktop to phone", 900, 320},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, view, w := opened(t, tt.before, store.NewMemory(1<<20))
			p.screen.visit(prado)
			position := p.screen.m.Center()
			badge := p.places.Badge
			p.showAbout()
			p.screen.describe("A background map update")
			w.Resize(fyne.NewSize(tt.after, 568))
			if w.Header.TitleWidget != nil || w.Header.Subtitle != "" {
				t.Fatal("background map update changed About's header")
			}
			test.Tap(p.back)
			narrow := tt.after <= viewswitcher.Breakpoint
			if view.Narrow() != narrow || (w.Header.TitleWidget == nil) != narrow || w.Header.Subtitle != p.screen.title() {
				t.Fatal("back did not restore the header and switcher")
			}
			if p.stack.Selected() != mapPage || p.screen.m.Center() != position || p.places.Badge != badge {
				t.Fatal("About navigation changed the map or Places badge")
			}
		})
	}
}
