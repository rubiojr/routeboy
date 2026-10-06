package main

import (
	"fmt"
	"image/color"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"code.rbel.co/rubiojr/fade/components/bottomsheet"
	"code.rbel.co/rubiojr/fade/components/button"
	"code.rbel.co/rubiojr/fade/components/label"
	"code.rbel.co/rubiojr/fade/components/mapview"
	"code.rbel.co/rubiojr/fade/icons"
	"code.rbel.co/rubiojr/fade/style"

	"github.com/rubiojr/routeboy/components/elevation"
)

const (
	// swatchSize is the dot in the track's color beside its name.
	swatchSize float32 = 12
	// sheetMargin is the space around the content of the sheet and the bar.
	sheetMargin = style.Gap18
	barMargin   = style.Gap12
)

// The numbers of a track, in the order of the grid.
const (
	statDistance = iota
	statAscent
	statDescent
	statHighest
	statLowest
	statTime
)

var statNames = [...]string{"Distance", "Ascent", "Descent", "Highest", "Lowest", "Time"}

// details shows the current track over the map: a bar with its name and
// length at the bottom, and a sheet with its numbers and elevation profile.
// It runs on the Fyne goroutine.
type details struct {
	sheet *bottomsheet.BottomSheet
	track *track

	bar        fyne.CanvasObject
	barSwatch  *canvas.Circle
	barTitle   *label.Label
	barSummary *label.Label

	body     fyne.CanvasObject
	swatch   *canvas.Circle
	title    *label.Label
	subtitle *label.Label
	stats    [len(statNames)]*label.Label
	profile  *elevation.Profile
	// samples are where the profile's samples lie.
	samples []mapview.Coordinate
	// note stands in for a profile that cannot be drawn.
	note *label.Label
	zoom *button.Button
	hide *button.Button

	// onCursor marks the point of the profile on the map, or clears it with nil.
	onCursor func(*mapview.Marker)
	onZoom   func(*track)
	onHide   func(*track)
}

// newDetails puts a sheet over content. It shows nothing until show has a track.
func newDetails(content fyne.CanvasObject) *details {
	d := &details{onCursor: func(*mapview.Marker) {}, onZoom: func(*track) {}, onHide: func(*track) {}}

	d.barSwatch = canvas.NewCircle(color.Transparent)
	d.barTitle = label.NewStyled("", label.Heading)
	d.barTitle.Truncation = fyne.TextTruncateEllipsis
	d.barSummary = label.NewStyled("", label.Caption)
	d.barSummary.Dimmed = true
	d.barSummary.Truncation = fyne.TextTruncateEllipsis
	up := canvas.NewImageFromResource(icons.Lookup("pan-up"))
	up.SetMinSize(fyne.NewSize(style.IconSize, style.IconSize))
	d.bar = container.New(layout.NewCustomPaddedLayout(barMargin, barMargin, sheetMargin, sheetMargin),
		container.NewBorder(nil, nil, swatch(d.barSwatch), container.NewCenter(up),
			container.New(layout.NewCustomPaddedVBoxLayout(2), d.barTitle, d.barSummary)))

	d.swatch = canvas.NewCircle(color.Transparent)
	d.title = label.NewStyled("", label.Title4)
	d.title.Truncation = fyne.TextTruncateEllipsis
	d.subtitle = label.NewStyled("", label.Caption)
	d.subtitle.Dimmed = true
	d.subtitle.Truncation = fyne.TextTruncateEllipsis
	header := container.NewBorder(nil, nil, swatch(d.swatch), nil,
		container.New(layout.NewCustomPaddedVBoxLayout(2), d.title, d.subtitle))

	tiles := make([]fyne.CanvasObject, len(statNames))
	for i, name := range statNames {
		d.stats[i] = label.NewStyled("", label.Heading)
		caption := label.NewStyled(name, label.Caption)
		caption.Dimmed = true
		tiles[i] = container.New(layout.NewCustomPaddedVBoxLayout(0), d.stats[i], caption)
	}
	grid := container.NewGridWithColumns(3, tiles...)

	d.profile = elevation.New(nil)
	d.profile.OnCursor = d.point
	d.note = label.NewDimmed("")
	d.note.Alignment = fyne.TextAlignCenter
	d.zoom = button.New("Zoom to Track", func() {
		if d.track != nil {
			d.sheet.SetOpen(false)
			d.onZoom(d.track)
		}
	})
	d.hide = button.New("Hide Track", func() {
		if d.track != nil {
			d.onHide(d.track)
		}
	})
	actions := container.NewGridWithColumns(2, d.zoom, d.hide)

	d.body = container.New(layout.NewCustomPaddedLayout(0, sheetMargin, sheetMargin, sheetMargin),
		container.New(layout.NewCustomPaddedVBoxLayout(sheetMargin), header, grid,
			container.NewStack(d.profile, container.NewCenter(d.note)), actions))

	d.sheet = bottomsheet.New(content, d.body)
	d.sheet.SetBottomBar(d.bar)
	d.sheet.SetRevealBottomBar(false)
	d.sheet.OnChanged = func(open bool) {
		if !open {
			d.clearCursor()
		}
	}
	return d
}

// swatch is a dot of a track's color, centred beside its name.
func swatch(dot *canvas.Circle) fyne.CanvasObject {
	return container.NewCenter(container.NewGridWrap(fyne.NewSize(swatchSize, swatchSize), dot))
}

// show fills the bar and the sheet with a track. Nil closes the sheet.
func (d *details) show(t *track) {
	if t != d.track {
		d.clearCursor()
	}
	d.track = t
	if t == nil {
		d.sheet.SetOpen(false)
		return
	}
	d.barSwatch.FillColor, d.swatch.FillColor = t.color, t.color
	d.barSwatch.Refresh()
	d.swatch.Refresh()
	d.barTitle.SetText(t.name)
	d.barSummary.SetText(t.summary())
	d.title.SetText(t.name)
	subtitle := filepath.Base(t.path)
	if !t.stats.Start.IsZero() {
		subtitle += " · " + t.stats.Start.Local().Format("Mon 2 Jan 2006, 15:04")
	}
	d.subtitle.SetText(subtitle)

	s := t.stats
	values := [len(statNames)]string{statDistance: distance(s.Distance), statTime: duration(s.Duration())}
	for _, i := range []int{statAscent, statDescent, statHighest, statLowest} {
		values[i] = "—"
	}
	if s.HasElevation {
		values[statAscent] = fmt.Sprintf("%.0f m", s.Ascent)
		values[statDescent] = fmt.Sprintf("%.0f m", s.Descent)
		values[statHighest] = fmt.Sprintf("%.0f m", s.Highest)
		values[statLowest] = fmt.Sprintf("%.0f m", s.Lowest)
	}
	for i, v := range values {
		d.stats[i].SetText(v)
	}

	var samples []elevation.Sample
	d.samples = nil
	switch {
	case t.shape == nil:
		d.note.SetText("Reading the track…")
	case len(t.shape.profile) < 2:
		d.note.SetText("No elevation in this track")
	default:
		d.note.SetText("")
		for _, p := range t.shape.profile {
			samples = append(samples, elevation.Sample{Distance: p.Distance, Elevation: p.Elevation})
			d.samples = append(d.samples, mapview.Coordinate{Latitude: p.Latitude, Longitude: p.Longitude})
		}
	}
	if d.note.Text == "" {
		d.note.Hide()
	} else {
		d.note.Show()
	}
	d.profile.Color = t.color
	if len(samples) != len(d.profile.Samples()) || d.profile.Cursor() < 0 {
		d.profile.SetSamples(samples)
	} else {
		d.profile.Refresh()
	}
}

// point marks the sample under the profile's cursor on the map.
func (d *details) point(i int) {
	if i < 0 || i >= len(d.samples) || d.track == nil {
		d.onCursor(nil)
		return
	}
	at := d.samples[i]
	d.onCursor(&mapview.Marker{Latitude: at.Latitude, Longitude: at.Longitude, Color: d.track.color})
}

func (d *details) clearCursor() {
	if d.profile.Cursor() >= 0 {
		d.profile.SetCursor(-1)
		d.onCursor(nil)
	}
}

// duration formats a time in hours and minutes, or a dash for none.
func duration(t time.Duration) string {
	if t <= 0 {
		return "—"
	}
	minutes := int(t.Round(time.Minute).Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%d h %02d min", minutes/60, minutes%60)
}
