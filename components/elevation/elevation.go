// Package elevation draws an elevation profile: the height of a track
// against the distance along it, with a cursor that follows the pointer or a
// finger.
//
//	profile := elevation.New(samples)
//	profile.OnCursor = func(i int) { markOnMap(i) }
//
// The line and the area beneath it use Color, or the accent. Grid lines and
// labels use the foreground: lines at the border opacity, labels dimmed and at
// caption size. The cursor reads out the distance and elevation above the plot.
package elevation

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"

	"code.rbel.co/rubiojr/fade/style"
)

const (
	// plotHeight is the least height of the plot itself.
	plotHeight float32 = 100
	minWidth   float32 = 160
	// gap separates the labels from the plot.
	gap float32 = 4
	// lineWidth strokes the profile; the area beneath is filled at areaOpacity.
	lineWidth   = 2
	areaOpacity = 0.25
	dotSize     = 10
	// elevationTicks and distanceTicks are about how many labels each axis gets.
	elevationTicks = 3
	distanceTicks  = 4
)

// Sample is a point of a profile, in meters.
type Sample struct {
	Distance, Elevation float64
}

// Profile is an elevation profile. Samples run in order of distance; it
// draws nothing with fewer than two.
type Profile struct {
	widget.BaseWidget

	// Color draws the line and, faded, the area beneath. Nil uses the accent.
	Color color.Color
	// OnCursor runs when the user moves the cursor, with the index of the
	// sample under it, or -1 when the pointer leaves.
	OnCursor func(index int)

	samples []Sample
	cursor  int
	r       *renderer
}

var (
	_ fyne.Tappable     = (*Profile)(nil)
	_ fyne.Draggable    = (*Profile)(nil)
	_ desktop.Hoverable = (*Profile)(nil)
)

// New creates a profile of samples.
func New(samples []Sample) *Profile {
	p := &Profile{cursor: -1, samples: append([]Sample(nil), samples...)}
	p.ExtendBaseWidget(p)
	return p
}

// Samples returns the samples drawn.
func (p *Profile) Samples() []Sample { return append([]Sample(nil), p.samples...) }

// SetSamples replaces the samples and removes the cursor.
func (p *Profile) SetSamples(samples []Sample) {
	p.samples = append([]Sample(nil), samples...)
	p.cursor = -1
	p.Refresh()
}

// Cursor is the index of the sample under the cursor, or -1.
func (p *Profile) Cursor() int { return p.cursor }

// SetCursor moves the cursor to a sample, or removes it with -1, without
// calling OnCursor.
func (p *Profile) SetCursor(index int) {
	if index < -1 || index >= len(p.samples) {
		index = -1
	}
	if index == p.cursor {
		return
	}
	p.cursor = index
	if p.r != nil {
		p.r.placeCursor()
	}
}

// point moves the cursor to the sample under x.
func (p *Profile) point(x float32) {
	if p.r == nil || len(p.samples) < 2 {
		return
	}
	i := p.r.sampleAt(x)
	if i == p.cursor {
		return
	}
	p.SetCursor(i)
	if p.OnCursor != nil {
		p.OnCursor(i)
	}
}

func (p *Profile) Tapped(e *fyne.PointEvent)        { p.point(e.Position.X) }
func (p *Profile) Dragged(e *fyne.DragEvent)        { p.point(e.Position.X) }
func (p *Profile) DragEnd()                         {}
func (p *Profile) MouseIn(e *desktop.MouseEvent)    { p.point(e.Position.X) }
func (p *Profile) MouseMoved(e *desktop.MouseEvent) { p.point(e.Position.X) }

func (p *Profile) MouseOut() {
	if p.cursor < 0 {
		return
	}
	p.SetCursor(-1)
	if p.OnCursor != nil {
		p.OnCursor(-1)
	}
}

// CreateRenderer implements fyne.Widget.
func (p *Profile) CreateRenderer() fyne.WidgetRenderer {
	r := &renderer{p: p, cursor: canvas.NewLine(color.Transparent), dot: canvas.NewCircle(color.Transparent),
		readout: canvas.NewText("", color.Transparent)}
	r.area = canvas.NewRaster(r.draw)
	r.dot.StrokeWidth = 2
	r.readout.TextSize = style.CaptionSize
	r.readout.TextStyle = fyne.TextStyle{Bold: true}
	p.r = r
	r.Refresh()
	return r
}

type renderer struct {
	p *Profile

	area    *canvas.Raster
	grid    []*canvas.Line
	heights []*canvas.Text // one per grid line
	lengths []*canvas.Text
	cursor  *canvas.Line
	dot     *canvas.Circle
	readout *canvas.Text
	objects []fyne.CanvasObject

	// The scale: elevations from low to high and distances from start to
	// end fill the plot.
	low, high, start, end float64
	ticks, marks          []float64
	plot                  fyne.Position
	plotSize              fyne.Size
	line                  color.NRGBA
}

func textHeight() float32 {
	return fyne.MeasureText("0", style.CaptionSize, fyne.TextStyle{}).Height
}

func (r *renderer) MinSize() fyne.Size {
	left := float32(0)
	for _, t := range r.heights {
		left = max(left, t.MinSize().Width)
	}
	return fyne.NewSize(max(minWidth, left+gap+60), plotHeight+2*(textHeight()+gap))
}

func (r *renderer) Layout(size fyne.Size) {
	th := textHeight()
	left := float32(0)
	for _, t := range r.heights {
		left = max(left, t.MinSize().Width)
	}
	if left > 0 {
		left += gap
	}
	r.plot = fyne.NewPos(left, th+gap)
	r.plotSize = fyne.NewSize(max(0, size.Width-left), max(0, size.Height-2*(th+gap)))
	// The line may run along the edges of the plot: leave it room.
	r.area.Move(r.plot.SubtractXY(lineWidth, lineWidth))
	r.area.Resize(r.plotSize.AddWidthHeight(2*lineWidth, 2*lineWidth))

	for i, tick := range r.ticks {
		y := r.y(tick)
		r.grid[i].Position1 = fyne.NewPos(r.plot.X, y)
		r.grid[i].Position2 = fyne.NewPos(r.plot.X+r.plotSize.Width, y)
		label := r.heights[i]
		w := label.MinSize().Width
		label.Move(fyne.NewPos(left-gap-w, y-th/2))
		label.Resize(fyne.NewSize(w, th))
	}
	// Distance labels are centred on their marks, kept on the widget and
	// left out where they would overlap the one before.
	right := float32(math.Inf(-1))
	for i, mark := range r.marks {
		label := r.lengths[i]
		w := label.MinSize().Width
		x := min(max(r.x(mark)-w/2, r.plot.X), size.Width-w)
		label.Move(fyne.NewPos(x, r.plot.Y+r.plotSize.Height+gap))
		label.Resize(fyne.NewSize(w, th))
		if x < right+gap*2 {
			label.Hide()
		} else {
			label.Show()
			right = x + w
		}
	}
	r.placeCursor()
}

// x is where a distance lies across the widget.
func (r *renderer) x(distance float64) float32 {
	if r.end <= r.start {
		return r.plot.X
	}
	return r.plot.X + float32((distance-r.start)/(r.end-r.start))*r.plotSize.Width
}

// y is where an elevation lies down the widget.
func (r *renderer) y(elevation float64) float32 {
	if r.high <= r.low {
		return r.plot.Y + r.plotSize.Height
	}
	return r.plot.Y + float32((r.high-elevation)/(r.high-r.low))*r.plotSize.Height
}

// sampleAt is the sample nearest to x.
func (r *renderer) sampleAt(x float32) int {
	samples := r.p.samples
	if r.plotSize.Width <= 0 || len(samples) == 0 {
		return -1
	}
	fraction := float64(min(max((x-r.plot.X)/r.plotSize.Width, 0), 1))
	distance := r.start + fraction*(r.end-r.start)
	i := sort.Search(len(samples), func(i int) bool { return samples[i].Distance >= distance })
	switch {
	case i == len(samples):
		return i - 1
	case i > 0 && distance-samples[i-1].Distance <= samples[i].Distance-distance:
		return i - 1
	}
	return i
}

func (r *renderer) placeCursor() {
	i := r.p.cursor
	if i < 0 || i >= len(r.p.samples) || len(r.p.samples) < 2 {
		r.cursor.Hide()
		r.dot.Hide()
		r.readout.Hide()
		return
	}
	s := r.p.samples[i]
	x, y := r.x(s.Distance), r.y(s.Elevation)
	r.cursor.Position1 = fyne.NewPos(x, r.plot.Y)
	r.cursor.Position2 = fyne.NewPos(x, r.plot.Y+r.plotSize.Height)
	r.dot.Move(fyne.NewPos(x-dotSize/2, y-dotSize/2))
	r.dot.Resize(fyne.NewSize(dotSize, dotSize))
	r.readout.Text = Distance(s.Distance-r.start) + " · " + Height(s.Elevation)
	w := r.readout.MinSize().Width
	r.readout.Move(fyne.NewPos(min(max(x-w/2, 0), r.p.Size().Width-w), 0))
	r.readout.Resize(fyne.NewSize(w, textHeight()))
	for _, o := range []fyne.CanvasObject{r.cursor, r.dot, r.readout} {
		o.Show()
		o.Refresh()
	}
}

// scale fits the axes to the samples.
func (r *renderer) scale() {
	samples := r.p.samples
	r.ticks, r.marks = nil, nil
	if len(samples) < 2 {
		r.low, r.high, r.start, r.end = 0, 0, 0, 0
		return
	}
	lowest, highest := math.Inf(1), math.Inf(-1)
	for _, s := range samples {
		lowest, highest = min(lowest, s.Elevation), max(highest, s.Elevation)
	}
	step := niceStep(highest-lowest, elevationTicks)
	r.low = math.Floor(lowest/step) * step
	r.high = math.Ceil(highest/step) * step
	if r.high <= r.low {
		r.high = r.low + step
	}
	for e := r.low; e <= r.high+step/2; e += step {
		r.ticks = append(r.ticks, e)
	}
	r.start, r.end = samples[0].Distance, samples[len(samples)-1].Distance
	if r.end > r.start {
		unit := 1.0
		if r.end-r.start >= 1000 {
			unit = 1000
		}
		step = niceStep((r.end-r.start)/unit, distanceTicks) * unit
		for d := 0.0; d <= r.end-r.start+step/1000; d += step {
			r.marks = append(r.marks, r.start+d)
		}
	}
}

// niceStep divides span in about count steps of 1, 2 or 5 times a power of ten.
func niceStep(span float64, count int) float64 {
	if span <= 0 || math.IsNaN(span) || math.IsInf(span, 0) {
		return 10
	}
	raw := span / float64(count)
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5} {
		if raw <= m*magnitude {
			return m * magnitude
		}
	}
	return 10 * magnitude
}

// Distance formats meters for reading: meters below a kilometer, else
// kilometers with one decimal.
func Distance(meters float64) string {
	if math.Abs(meters) < 1000 {
		return fmt.Sprintf("%.0f m", meters)
	}
	return fmt.Sprintf("%.1f km", meters/1000)
}

// Height formats an elevation in whole meters.
func Height(meters float64) string { return fmt.Sprintf("%.0f m", meters) }

// mark formats a distance label: whole kilometers or meters where they will do.
func mark(meters, span float64) string {
	if span < 1000 {
		return fmt.Sprintf("%.0f m", meters)
	}
	return fmt.Sprintf("%s km", trim(meters/1000))
}

func trim(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%g", math.Round(v*10)/10)
}

func (r *renderer) Refresh() {
	p := r.p
	th, v := p.Theme(), style.Variant()
	fg := th.Color(theme.ColorNameForeground, v)
	line := p.Color
	if line == nil {
		line = th.Color(style.ColorNameAccentBG, v)
	}
	r.line = style.ToNRGBA(line)
	r.scale()

	for len(r.grid) < len(r.ticks) {
		r.grid = append(r.grid, canvas.NewLine(color.Transparent))
		r.heights = append(r.heights, canvas.NewText("", color.Transparent))
	}
	r.grid, r.heights = r.grid[:len(r.ticks)], r.heights[:len(r.ticks)]
	for i, tick := range r.ticks {
		r.grid[i].StrokeColor, r.grid[i].StrokeWidth = style.Border(fg), 1
		r.heights[i].Text, r.heights[i].Color, r.heights[i].TextSize = Height(tick), style.Dimmed(fg), style.CaptionSize
		r.heights[i].Refresh()
	}
	for len(r.lengths) < len(r.marks) {
		r.lengths = append(r.lengths, canvas.NewText("", color.Transparent))
	}
	r.lengths = r.lengths[:len(r.marks)]
	for i, m := range r.marks {
		r.lengths[i].Text, r.lengths[i].Color, r.lengths[i].TextSize = mark(m-r.start, r.end-r.start), style.Dimmed(fg), style.CaptionSize
		r.lengths[i].Refresh()
	}
	r.cursor.StrokeColor, r.cursor.StrokeWidth = style.Dimmed(fg), 1
	r.dot.FillColor, r.dot.StrokeColor = r.line, th.Color(theme.ColorNameBackground, v)
	r.readout.Color = fg

	r.objects = r.objects[:0]
	for _, g := range r.grid {
		r.objects = append(r.objects, g)
	}
	r.objects = append(r.objects, r.area)
	for _, t := range r.heights {
		r.objects = append(r.objects, t)
	}
	for _, t := range r.lengths {
		r.objects = append(r.objects, t)
	}
	r.objects = append(r.objects, r.cursor, r.dot, r.readout)
	r.Layout(p.Size())
	r.area.Refresh()
	for _, g := range r.grid {
		g.Refresh()
	}
	r.cursor.Refresh()
	r.dot.Refresh()
}

// draw fills the area beneath the profile and strokes its line, at the
// raster's size in pixels.
func (r *renderer) draw(width, height int) image.Image {
	samples := r.p.samples
	if width <= 0 || height <= 0 || len(samples) < 2 || r.end <= r.start || r.high <= r.low || r.plotSize.Width <= 0 {
		return image.NewUniform(color.Transparent)
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	scanner := rasterx.NewScannerGV(width, height, target, target.Bounds())
	scale := float64(width) / float64(r.plotSize.Width+2*lineWidth)
	pad := lineWidth * scale
	across, down := float64(width)-2*pad, float64(height)-2*pad
	at := func(s Sample) fixed.Point26_6 {
		x := pad + (s.Distance-r.start)/(r.end-r.start)*across
		y := pad + (r.high-s.Elevation)/(r.high-r.low)*down
		return fixed.Point26_6{X: fixed.Int26_6(math.Round(x * 64)), Y: fixed.Int26_6(math.Round(y * 64))}
	}
	// Keep one sample in each half pixel, and the last.
	points := make([]fixed.Point26_6, 0, min(len(samples), 2*width+2))
	for i, s := range samples {
		p := at(s)
		if n := len(points); n > 0 && i < len(samples)-1 && p.X-points[n-1].X < 32 {
			continue
		}
		points = append(points, p)
	}
	bottom := fixed.Int26_6(math.Round((pad + down) * 64))

	filler := rasterx.NewFiller(width, height, scanner)
	filler.SetColor(style.Alpha(r.line, areaOpacity))
	filler.Start(fixed.Point26_6{X: points[0].X, Y: bottom})
	for _, p := range points {
		filler.Line(p)
	}
	filler.Line(fixed.Point26_6{X: points[len(points)-1].X, Y: bottom})
	filler.Stop(true)
	filler.Draw()

	stroke := rasterx.NewStroker(width, height, scanner)
	stroke.SetColor(r.line)
	stroke.SetStroke(fixed.Int26_6(lineWidth*scale*64), 0, rasterx.RoundCap, nil, rasterx.RoundGap, rasterx.Round)
	stroke.Start(points[0])
	for _, p := range points[1:] {
		stroke.Line(p)
	}
	stroke.Stop(false)
	stroke.Draw()
	return target
}

func (r *renderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *renderer) Destroy()                     {}
