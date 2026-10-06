package main

import (
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"code.rbel.co/rubiojr/fade/components/boxedlist"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/check"
	"code.rbel.co/rubiojr/fade/components/entry"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/components/preferences"
	"code.rbel.co/rubiojr/fade/icons"
	"code.rbel.co/rubiojr/fade/style"

	"github.com/rubiojr/routeboy/gpxtrack"
)

const (
	// tracksShown and trackPoints keep the map within its limits: each
	// track is drawn with at most trackPoints points, the current one twice.
	tracksShown = 20
	trackPoints = 4000
	// profileSamples is about the most samples a profile is drawn with.
	profileSamples = 1000
	// tracksListed bounds the files read from the folder.
	tracksListed = 2000
	// pickDistance is how near a tap must be to a track to choose it.
	pickDistance = 24
	// rowName and folderName keep the list narrow enough for a phone: a
	// row can't be narrower than its longest word.
	rowName    = 40
	folderName = 24
	shownKey   = "tracks.shown"
	currentKey = "tracks.current"
	// trackWidth and currentWidth stroke the tracks; the current one has a
	// white casing currentCasing wide.
	trackWidth    = 4
	currentWidth  = 5
	currentCasing = 9
)

// trackColors tell the tracks on the map apart. A track's profile uses its color.
var trackColors = []color.NRGBA{style.Blue3, style.Red2, style.Green5, style.Purple2, style.Orange4, style.Brown2, style.Dark2}

// track is a GPX file of the folder. Its shape is read when it is shown.
type track struct {
	path     string
	name     string
	modified time.Time
	size     int64
	err      error
	stats    gpxtrack.Stats
	// search is the name and the file name, folded for searching.
	search string

	shape   *shape
	reading bool
	color   color.NRGBA
	row     *boxedlist.ActionRow
	check   *check.Check
}

// shape is what drawing a track needs.
type shape struct {
	lines   [][]mapview.Coordinate
	profile []gpxtrack.Sample
}

// summary is the length, climb and date of a track.
func (t *track) summary() string {
	if t.err != nil {
		return "Not a track: " + reason(t.err)
	}
	parts := []string{distance(t.stats.Distance)}
	if t.stats.HasElevation {
		parts = append(parts, fmt.Sprintf("↑ %.0f m", t.stats.Ascent))
	}
	if !t.stats.Start.IsZero() {
		parts = append(parts, t.stats.Start.Local().Format("2 Jan 2006"))
	}
	return strings.Join(parts, " · ")
}

// distance formats meters: whole meters below a kilometer, else kilometers.
func distance(meters float64) string {
	if meters < 1000 {
		return fmt.Sprintf("%.0f m", meters)
	}
	return fmt.Sprintf("%.1f km", meters/1000)
}

// library lists the GPX files of a folder, searches them and keeps the
// tracks shown on the map. It runs on the Fyne goroutine.
type library struct {
	dir   string
	prefs fyne.Preferences
	// do hands a function to the Fyne goroutine.
	do func(func())

	all []*track
	// shown are the tracks on the map, in the order they were chosen.
	shown   []*track
	current *track
	// restore names the tracks shown when the application last ran, until
	// the folder is read.
	restore []string
	read    bool
	reading bool
	again   bool
	// folder says why the folder could not be read, problem why a track
	// could not be shown.
	folder  error
	problem string

	search  *entry.Entry
	group   *preferences.Group
	page    *preferences.Page
	list    *boxedlist.List
	content fyne.CanvasObject

	// changed draws the tracks shown and the current one. An error undoes the
	// last change.
	changed func() error
	// showing moves the map to a track.
	showing func(*track)
	// counted receives the number of tracks shown.
	counted func(int)

	workers sync.WaitGroup
	closed  bool
}

func newLibrary(dir string, prefs fyne.Preferences) *library {
	l := &library{dir: dir, prefs: prefs, do: fyne.Do, list: boxedlist.New(),
		changed: func() error { return nil }, showing: func(*track) {}, counted: func(int) {}}
	l.restore = prefs.StringList(shownKey)
	l.search = entry.NewSearch("Search tracks")
	l.search.OnChanged = func(string) { l.filter() }
	l.group = preferences.NewGroup("Tracks", "Check a track to show it on the map.", l.list)
	l.page = preferences.New(l.group)
	l.list.OnLayoutChanged = l.page.Refresh
	l.content = container.NewBorder(container.New(clamp{}, l.search), nil, nil, nil, l.page)
	l.filter()
	return l
}

// load reads the folder again in the background. Files read before are
// read again only if they changed.
func (l *library) load() {
	if l.closed {
		return
	}
	if l.reading {
		l.again = true
		return
	}
	l.reading = true
	known := make(map[string]*track, len(l.all))
	for _, t := range l.all {
		known[t.path] = t
	}
	dir := l.dir
	l.workers.Go(func() {
		found, err := scan(dir, known)
		l.do(func() { l.loaded(found, err) })
	})
}

// loaded lists the tracks found and keeps those shown that still exist.
func (l *library) loaded(found []*track, err error) {
	l.reading = false
	if l.closed {
		return
	}
	shown, current := l.kept(found)
	l.all, l.folder = found, err
	l.read, l.restore = true, nil
	l.shown, l.current = nil, nil
	for _, t := range shown {
		if t.color == (color.NRGBA{}) {
			t.color = l.freeColor()
		}
		l.shown = append(l.shown, t)
		if t.path == current {
			l.current = t
		}
		l.readShape(t)
	}
	if l.current == nil && len(l.shown) > 0 {
		l.current = l.shown[len(l.shown)-1]
	}
	l.update(true)
	if l.again {
		l.again = false
		l.load()
	}
}

// kept are the tracks found that stay shown, with the path of the current
// one: those shown before the folder was read again, or when it is first
// read, those shown when the application last ran. A changed file keeps the
// color of the track it replaces.
func (l *library) kept(found []*track) (shown []*track, current string) {
	byPath := make(map[string]*track, len(found))
	for _, t := range found {
		if t.err == nil {
			byPath[t.path] = t
		}
	}
	if !l.read {
		for _, path := range l.restore {
			if t := byPath[path]; t != nil && !slices.Contains(shown, t) && len(shown) < tracksShown {
				shown = append(shown, t)
			}
		}
		return shown, l.prefs.String(currentKey)
	}
	for _, old := range l.shown {
		if t := byPath[old.path]; t != nil {
			t.color = old.color
			shown = append(shown, t)
		}
	}
	if l.current != nil {
		current = l.current.path
	}
	return shown, current
}

// scan lists the GPX files under a folder, newest first, and measures those
// it doesn't know. Hidden folders are left out.
func scan(dir string, known map[string]*track) ([]*track, error) {
	var found []*track
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil && path == dir:
			return err
		case err != nil:
			return nil
		case d.IsDir() && path != dir && strings.HasPrefix(d.Name(), "."):
			return filepath.SkipDir
		case d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".gpx"):
			return nil
		case len(found) == tracksListed:
			return fs.SkipAll
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if t := known[path]; t != nil && t.modified.Equal(info.ModTime()) && t.size == info.Size() {
			found = append(found, t)
		} else {
			found = append(found, measure(path, info))
		}
		return nil
	})
	slices.SortStableFunc(found, func(a, b *track) int {
		if c := b.when().Compare(a.when()); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	return found, err
}

// when is the time a track was recorded, or when its file changed.
func (t *track) when() time.Time {
	if !t.stats.Start.IsZero() {
		return t.stats.Start
	}
	return t.modified
}

// measure reads a GPX file for its name and numbers.
func measure(path string, info fs.FileInfo) *track {
	t := &track{path: path, modified: info.ModTime(), size: info.Size()}
	g, err := readTrack(path)
	t.err, t.stats, t.name = err, g.Stats, g.Name
	if t.name == "" {
		t.name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	t.search = fold(t.name + " " + filepath.Base(path))
	return t
}

func readTrack(path string) (gpxtrack.Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return gpxtrack.Track{}, err
	}
	defer f.Close()
	return gpxtrack.Read(f)
}

// newShape thins a track for drawing.
func newShape(g gpxtrack.Track) *shape {
	s := &shape{profile: gpxtrack.Thin(g.Profile(), profileSamples)}
	for _, segment := range g.Simplify(trackPoints) {
		line := make([]mapview.Coordinate, len(segment))
		for i, p := range segment {
			line[i] = mapview.Coordinate{Latitude: p.Latitude, Longitude: p.Longitude}
		}
		s.lines = append(s.lines, line)
	}
	return s
}

// readShape reads what drawing a track needs, in the background.
func (l *library) readShape(t *track) {
	if t.shape != nil || t.reading || l.closed {
		return
	}
	t.reading = true
	path := t.path
	l.workers.Go(func() {
		g, err := readTrack(path)
		var s *shape
		if err == nil {
			s = newShape(g)
		}
		l.do(func() {
			t.reading = false
			if l.closed {
				return
			}
			if err != nil {
				l.toggle(t, false)
				l.problem = fmt.Sprintf("%s could not be read: %s", short(t.name, rowName), reason(err))
				l.update(true)
				return
			}
			t.shape = s
			if !slices.Contains(l.shown, t) {
				return
			}
			if err := l.changed(); err != nil {
				l.toggle(t, false)
				l.problem = fmt.Sprintf("%s could not be shown: %s", short(t.name, rowName), reason(err))
				l.update(true)
			}
		})
	})
}

// fold lowers a text and drops its accents, for searching.
func fold(text string) string {
	folding := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(folding, strings.ToLower(text))
	if err != nil {
		return strings.ToLower(text)
	}
	return folded
}

// filter lists the tracks that match the search, with what stands in the
// way of showing any.
func (l *library) filter() {
	var rows []boxedlist.Row
	if l.problem != "" {
		row := boxedlist.NewActionRow(l.problem, "")
		row.SetIcon(icons.Raw("dialog-warning"))
		rows = append(rows, row)
	}
	switch {
	case errors.Is(l.folder, fs.ErrNotExist):
		rows = append(rows, boxedlist.NewActionRow("No tracks folder", "Make "+shortPath(l.dir)+" and put GPX files in it."))
	case l.folder != nil:
		rows = append(rows, boxedlist.NewActionRow("The tracks folder could not be read", reason(l.folder)))
	case !l.read:
		rows = append(rows, boxedlist.NewActionRow("Looking for tracks…", ""))
	case len(l.all) == 0:
		rows = append(rows, boxedlist.NewActionRow("No tracks yet", "Put GPX files in "+shortPath(l.dir)+"."))
	}
	words := strings.Fields(fold(l.search.Text))
	matched := 0
	for _, t := range l.all {
		if matches(t.search, words) {
			rows = append(rows, l.row(t))
			matched++
		}
	}
	if matched == 0 && len(l.all) > 0 {
		rows = append(rows, boxedlist.NewActionRow("No tracks match", "Search names and file names."))
	}
	l.list = boxedlist.New(rows...)
	l.list.OnLayoutChanged = l.page.Refresh
	l.group.Content = l.list
	l.group.Refresh()
	l.page.Refresh()
}

// reason is what went wrong, without the path of the file it went wrong with.
func reason(err error) string {
	var path *fs.PathError
	if errors.As(err, &path) {
		return path.Err.Error()
	}
	return err.Error()
}

// shortPath names a folder briefly: the home folder as ~, and a long path by
// its last part.
func shortPath(dir string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, err := filepath.Rel(home, dir); err == nil && rest != ".." && !strings.HasPrefix(rest, ".."+string(filepath.Separator)) {
			dir = filepath.Join("~", rest)
		}
	}
	if len([]rune(dir)) > folderName {
		dir = "…" + string(filepath.Separator) + filepath.Base(dir)
	}
	return dir
}

// matches reports whether a text has every word.
func matches(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// row is the row of a track: activating it checks the track, and its button
// shows the track on the map.
func (l *library) row(t *track) *boxedlist.ActionRow {
	if t.row != nil {
		return t.row
	}
	t.row = boxedlist.NewActionRow(short(t.name, rowName), t.summary())
	if t.err != nil {
		t.row.SetIcon(icons.Raw("dialog-warning"))
		return t.row
	}
	t.check = check.New("", func(on bool) { l.setShown(t, on) })
	t.check.SetChecked(slices.Contains(l.shown, t))
	t.row.AddPrefix(t.check)
	t.row.SetActivatable(func() { l.setShown(t, !slices.Contains(l.shown, t)) })
	show := button.NewIcon(icons.Raw("mark-location"), func() { l.show(t) })
	show.Style = button.Flat
	t.row.AddSuffix(show)
	return t.row
}

// setShown puts a track on the map, as the current one, or takes it off.
func (l *library) setShown(t *track, on bool) {
	problem := l.toggle(t, on)
	listed := problem != l.problem
	l.problem = problem
	l.update(listed)
}

// toggle puts a track on the map or takes it off, and says why it could not.
func (l *library) toggle(t *track, on bool) (problem string) {
	i := slices.Index(l.shown, t)
	if on == (i >= 0) {
		return ""
	}
	if on && len(l.shown) == tracksShown {
		return fmt.Sprintf("At most %d tracks can be shown at once.", tracksShown)
	}
	shown, current := slices.Clone(l.shown), l.current
	if on {
		t.color = l.freeColor()
		l.shown = append(l.shown, t)
		l.current = t
		l.readShape(t)
	} else {
		l.shown = slices.Delete(l.shown, i, i+1)
		if l.current == t {
			l.current = nil
			if n := len(l.shown); n > 0 {
				l.current = l.shown[n-1]
			}
		}
	}
	if err := l.changed(); err != nil {
		l.shown, l.current = shown, current
		_ = l.changed()
		return fmt.Sprintf("%s could not be shown: %s", short(t.name, rowName), reason(err))
	}
	return ""
}

// setCurrent makes a track shown the current one.
func (l *library) setCurrent(t *track) {
	if t == l.current || !slices.Contains(l.shown, t) {
		return
	}
	l.current = t
	l.update(false)
}

// show puts a track on the map as the current one and moves the map to it.
func (l *library) show(t *track) {
	if !slices.Contains(l.shown, t) {
		l.setShown(t, true)
		if !slices.Contains(l.shown, t) {
			return
		}
	}
	l.setCurrent(t)
	l.showing(t)
}

// update follows a change of the tracks shown everywhere they show, and
// lists the tracks again if listed is set.
func (l *library) update(listed bool) {
	for _, t := range l.all {
		l.mark(t)
	}
	l.save()
	l.counted(len(l.shown))
	if err := l.changed(); err != nil {
		l.problem, listed = err.Error(), true
	}
	if listed {
		l.filter()
	}
}

// mark checks the row of a track if it is shown.
func (l *library) mark(t *track) {
	if t.check != nil {
		t.check.SetChecked(slices.Contains(l.shown, t))
	}
}

// save keeps the tracks shown for the next run.
func (l *library) save() {
	if !l.read {
		return
	}
	paths := make([]string, len(l.shown))
	for i, t := range l.shown {
		paths[i] = t.path
	}
	l.prefs.SetStringList(shownKey, paths)
	current := ""
	if l.current != nil {
		current = l.current.path
	}
	l.prefs.SetString(currentKey, current)
}

// freeColor is the first color no track shown has.
func (l *library) freeColor() color.NRGBA {
	for _, c := range trackColors {
		if !slices.ContainsFunc(l.shown, func(t *track) bool { return t.color == c }) {
			return c
		}
	}
	return trackColors[len(l.shown)%len(trackColors)]
}

// lines are the tracks to draw: the current one on top, with a casing.
func (l *library) lines() []mapview.Track {
	var lines []mapview.Track
	for _, t := range l.shown {
		if t != l.current && t.shape != nil {
			lines = append(lines, mapview.Track{Name: t.name, Segments: t.shape.lines, Color: t.color, Width: trackWidth})
		}
	}
	if t := l.current; t != nil && t.shape != nil {
		lines = append(lines,
			mapview.Track{Name: t.name, Segments: t.shape.lines, Color: style.White, Width: currentCasing},
			mapview.Track{Name: t.name, Segments: t.shape.lines, Color: t.color, Width: currentWidth})
	}
	return lines
}

// nearest is the track shown that passes closest to a point of the map,
// within pickDistance, or nil.
func (l *library) nearest(m *mapview.Map, at fyne.Position) *track {
	var found *track
	best := float32(pickDistance)
	for _, t := range l.shown {
		if t.shape == nil {
			continue
		}
		for _, line := range t.shape.lines {
			var previous fyne.Position
			for i, p := range line {
				point := m.Locate(p.Latitude, p.Longitude)
				d := distanceTo(at, point, point)
				if i > 0 {
					d = distanceTo(at, previous, point)
				}
				if d < best || d == best && t == l.current {
					found, best = t, d
				}
				previous = point
			}
		}
	}
	return found
}

// distanceTo is how far p lies from the line between a and b.
func distanceTo(p, a, b fyne.Position) float32 {
	dx, dy := b.X-a.X, b.Y-a.Y
	along := float32(0)
	if length := dx*dx + dy*dy; length > 0 {
		along = max(0, min(1, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/length))
	}
	x, y := a.X+along*dx-p.X, a.Y+along*dy-p.Y
	return float32(math.Hypot(float64(x), float64(y)))
}

// close stops reading. Call it when the window closes.
func (l *library) close() { l.closed = true }

// clamp lays a search entry out above a preferences page, as wide as the
// page's content.
type clamp struct{}

func (clamp) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := objects[0].MinSize()
	return fyne.NewSize(size.Width+2*style.Gap12, size.Height+style.Gap12)
}

func (clamp) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	width := min(size.Width-2*style.Gap12, 600)
	objects[0].Move(fyne.NewPos((size.Width-width)/2, style.Gap12))
	objects[0].Resize(fyne.NewSize(width, objects[0].MinSize().Height))
}
