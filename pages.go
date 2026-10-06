package main

import (
	_ "embed"
	"fmt"
	"time"

	"fyne.io/fyne/v2"

	"code.rbel.co/rubiojr/fade/components/base"
	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/mapview/oms"
	"code.rbel.co/rubiojr/fade/components/mapview/store"
	"code.rbel.co/rubiojr/fade/components/menubutton"
	"code.rbel.co/rubiojr/fade/components/preferences"
	"code.rbel.co/rubiojr/fade/components/viewswitcher"
	"code.rbel.co/rubiojr/fade/components/window"
	"code.rbel.co/rubiojr/fade/icons"
)

// The pages, in the order of the switcher.
const (
	mapPage = iota
	tracksPage
	bookmarksPage
	placesPage
)

const (
	// placesKept is the most places the Places page lists.
	placesKept = 12
	// barSlide is how long the bottom bar takes to slide in or out.
	barSlide = 250 * time.Millisecond
)

//go:embed icons/route-symbolic.svg
var routeIcon []byte

// pages owns the map, the tracks, bookmarks, recent places and the About
// page. It runs on the Fyne goroutine.
type pages struct {
	stack  *viewswitcher.Stack
	screen *screen

	tracks     *library
	tracksView *viewswitcher.Page
	details    *details
	sliding    *fyne.Animation

	places  *viewswitcher.Page
	list    *boxedlist.List
	visited []oms.Place
	// unseen counts the places visited since the Places page was last open.
	unseen int

	saved           *viewswitcher.Page
	bookmarkList    *boxedlist.List
	bookmarkProblem string
	prefs           fyne.Preferences

	data  store.Store
	kept  *boxedlist.ActionRow
	about *preferences.Page

	w               *window.Window
	view            *viewswitcher.View
	menu            *menubutton.MenuButton
	back            *button.Button
	mainTitle       string
	mainTitleWidget fyne.CanvasObject
}

// newPages puts a screen beside the tracks, bookmarks and recent places, with
// map data in About. The tracks are read once load is called.
func newPages(s *screen, data store.Store, from settings, prefs fyne.Preferences) *pages {
	p := &pages{stack: viewswitcher.NewStack(), screen: s, list: boxedlist.New(),
		bookmarkList: boxedlist.New(), data: data, prefs: prefs}
	s.visited = p.remember
	s.addBookmark.OnTapped = p.addBookmark

	p.tracks = newLibrary(from.tracks, prefs)
	p.details = newDetails(s)
	p.tracks.changed = p.drawTracks
	p.tracks.showing = p.showTrack
	p.details.onCursor = s.setCursor
	p.details.onZoom = p.fitTrack
	p.details.onHide = func(t *track) { p.tracks.setShown(t, false) }
	s.covered = p.details.sheet.BottomBarHeight
	s.m.OnTapped = p.pick

	network := "Map data and search come from the server"
	if from.offline {
		network = "Offline: the map uses kept data only"
	}
	p.kept = boxedlist.NewActionRow("Kept data", "")
	about := boxedlist.New(boxedlist.NewActionRow("Server", from.server), p.kept,
		boxedlist.NewActionRow("Network", network), boxedlist.NewActionRow("Tracks folder", from.tracks))

	p.stack.Add("Map", icons.Raw("mark-location"), p.details.sheet)
	p.tracksView = p.stack.Add("Tracks", &fyne.StaticResource{StaticName: "route-symbolic.svg", StaticContent: routeIcon}, p.tracks.content)
	p.tracks.counted = p.tracksView.SetBadge
	p.saved = p.stack.Add("Bookmarks", icons.Raw("starred"), preferences.New(
		preferences.NewGroup("Bookmarks", "Saved places. Choose one to return to its position and zoom.", p.bookmarkList)))
	p.places = p.stack.Add("Places", icons.Raw("mark-location"), preferences.New(
		preferences.NewGroup("Visited", "Places chosen from a search. Choose one to return to it.", p.list)))
	p.about = preferences.New(preferences.NewGroup("Map and tracks", "", about))
	p.about.Hide()
	p.stack.OnChanged = p.opened
	p.list.Add(boxedlist.NewActionRow("No places yet", "Search the map and choose a place"))
	if err := p.loadBookmarks(); err != nil {
		p.bookmarkProblem = err.Error()
	}
	p.listBookmarks()
	s.updateBookmarkButton()
	p.measure()
	return p
}

// opened follows the user to a page. Opening Tracks looks for new files.
func (p *pages) opened(index int) {
	if index == tracksPage {
		p.tracks.load()
	}
	if index == placesPage {
		p.unseen = 0
		p.places.SetBadge(0)
		p.places.SetNeedsAttention(false)
	}
}

// mount installs the main views and the header's menu and back navigation.
func (p *pages) mount(w *window.Window) *viewswitcher.View {
	p.w = w
	p.view = viewswitcher.NewView(p.stack, w.Header)
	p.menu = menubutton.New(menubutton.Item{Label: "About Routeboy", Icon: icons.Raw("dialog-information"), OnActivated: p.showAbout})
	p.back = button.NewIcon(icons.Raw("go-previous"), p.showMain)
	p.back.Hide()
	w.Header.PackStart(p.back)
	w.Header.PackEnd(p.menu)
	// The map can finish a move while About is open. Keep its coordinates
	// out of About's header, then show the current position on return.
	describe := p.screen.describe
	p.screen.describe = func(text string) {
		if !p.about.Visible() {
			describe(text)
		}
	}
	w.SetContent(p.view)
	return p.view
}

func (p *pages) showAbout() {
	p.measure()
	if p.about.Visible() {
		return
	}
	h := p.w.Header
	p.mainTitle, p.mainTitleWidget = h.Title, h.TitleWidget
	p.menu.Hide()
	p.view.Hide()
	p.about.Show()
	p.about.ScrollToTop()
	p.back.Show()
	h.Title, h.Subtitle, h.TitleWidget = "About Routeboy", "", nil
	p.w.SetContent(p.about)
	h.Refresh()
	base.Focus(p.w.Canvas(), p.back)
}

func (p *pages) showMain() {
	if !p.about.Visible() {
		return
	}
	p.about.Hide()
	p.back.Hide()
	p.menu.Show()
	h := p.w.Header
	h.Title, h.TitleWidget = p.mainTitle, p.mainTitleWidget
	p.view.Show()
	p.w.SetContent(p.view)
	p.screen.describe(p.screen.title())
	h.Refresh()
	base.Focus(p.w.Canvas(), p.menu)
}

// measure tells how much map data is kept.
func (p *pages) measure() {
	text := "Not measured by this store"
	if sized, ok := p.data.(interface{ Size() int64 }); ok {
		text = fmt.Sprintf("%.1f MiB", float64(sized.Size())/(1<<20))
	}
	p.kept.SetSubtitle(text)
}

// remember lists a place visited, the latest first, and counts it on the
// Places page until that page is opened.
func (p *pages) remember(place oms.Place) {
	known := false
	latest := []oms.Place{place}
	for _, other := range p.visited {
		if same(other, place) {
			known = true
			continue
		}
		latest = append(latest, other)
	}
	p.visited = latest[:min(len(latest), placesKept)]

	for _, row := range p.list.Rows() {
		p.list.Remove(row)
	}
	for _, place := range p.visited {
		row := boxedlist.NewActionRow(place.Name, detail(place))
		row.SetActivatable(func() { p.visit(place) })
		p.list.Add(row)
	}
	if !known && p.stack.Selected() != placesPage {
		p.unseen++
		p.places.SetBadge(p.unseen)
		p.places.SetNeedsAttention(true)
	}
}

// same reports whether two places are one.
func same(a, b oms.Place) bool {
	return a.Name == b.Name && a.Latitude == b.Latitude && a.Longitude == b.Longitude
}

// visit shows the map at a place.
func (p *pages) visit(place oms.Place) {
	p.stack.Select(mapPage)
	p.screen.visit(place)
}

// close stops the map, the searches and the reading of tracks. Call it when
// the window closes.
func (p *pages) close() {
	p.tracks.close()
	p.screen.close()
}

// drawTracks draws the tracks shown, and the current one in the bottom bar.
func (p *pages) drawTracks() error {
	if err := p.screen.m.SetTracks(p.tracks.lines()...); err != nil {
		return err
	}
	current := p.tracks.current
	p.details.show(current)
	p.revealBar(current != nil)
	return nil
}

// revealBar slides the bottom bar in or out. The controls over the map follow
// it, and the map ends above it once it is in.
func (p *pages) revealBar(reveal bool) {
	sheet := p.details.sheet
	if reveal == sheet.RevealBottomBar() {
		return
	}
	if !reveal {
		sheet.SetOpen(false)
		p.screen.setInset(0)
	}
	sheet.SetRevealBottomBar(reveal)
	if p.sliding != nil {
		p.sliding.Stop()
	}
	p.sliding = fyne.NewAnimation(barSlide, func(done float32) {
		p.screen.relayout()
		if done >= 1 && sheet.RevealBottomBar() {
			p.screen.setInset(p.details.bar.MinSize().Height)
		}
	})
	p.sliding.Start()
}

// pick makes the track tapped on the map the current one.
func (p *pages) pick(at mapview.Coordinate) {
	m := p.screen.m
	if t := p.tracks.nearest(m, m.Locate(at.Latitude, at.Longitude)); t != nil {
		p.tracks.setCurrent(t)
	}
}

// showTrack moves to the map and fits a track in it.
func (p *pages) showTrack(t *track) {
	p.stack.Select(mapPage)
	p.fitTrack(t)
}

// fitTrack shows a track whole on the map.
func (p *pages) fitTrack(t *track) {
	p.details.sheet.SetOpen(false)
	p.screen.fit(t.stats.South, t.stats.West, t.stats.North, t.stats.East)
}
