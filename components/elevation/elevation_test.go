package elevation

import (
	"image/color"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"code.rbel.co/rubiojr/fade/style"
)

// climb rises from 612 m to 980 m over 12 km.
var climb = []Sample{{0, 612}, {3000, 700}, {6000, 850}, {9000, 980}, {12000, 900}}

func shown(t *testing.T, samples []Sample) (*Profile, fyne.Window) {
	t.Helper()
	a := test.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(style.NewTheme(style.WithScheme(style.SchemeLight), style.WithAccent(style.AccentBlue)))
	p := New(samples)
	w := test.NewTempWindow(t, p)
	w.SetPadded(false)
	w.Resize(fyne.NewSize(400, 160))
	return p, w
}

func TestAxesFitTheSamples(t *testing.T) {
	p, _ := shown(t, climb)
	r := p.r
	if !slices.Equal(r.ticks, []float64{600, 800, 1000}) {
		t.Errorf("elevation lines at %v", r.ticks)
	}
	var heights, lengths []string
	for _, h := range r.heights {
		heights = append(heights, h.Text)
	}
	for _, l := range r.lengths {
		if l.Visible() {
			lengths = append(lengths, l.Text)
		}
	}
	if !slices.Equal(heights, []string{"600 m", "800 m", "1000 m"}) {
		t.Errorf("elevation labels %q", heights)
	}
	if !slices.Equal(lengths, []string{"0 km", "5 km", "10 km"}) {
		t.Errorf("distance labels %q", lengths)
	}
	if got, want := p.MinSize().Height, plotHeight+2*(textHeight()+gap); got != want {
		t.Errorf("height %v, want %v", got, want)
	}
	// The highest line is the top of the plot and the lowest its bottom.
	if top, bottom := r.grid[2].Position1.Y, r.grid[0].Position1.Y; top != r.plot.Y || bottom != r.plot.Y+r.plotSize.Height {
		t.Errorf("lines from %v to %v, plot %v %v", top, bottom, r.plot, r.plotSize)
	}
}

func TestCursorFollowsThePointer(t *testing.T) {
	p, _ := shown(t, climb)
	var reported []int
	p.OnCursor = func(i int) { reported = append(reported, i) }
	r := p.r
	at := func(distance float64) fyne.Position { return fyne.NewPos(r.x(distance), r.plot.Y+10) }

	p.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(5000)}})
	p.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(5500)}}) // the same sample
	p.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at(11000)}})
	test.TapAt(p, fyne.NewPos(-50, 0)) // left of the plot: the first sample
	if !slices.Equal(reported, []int{2, 4, 0}) {
		t.Fatalf("cursor at %v, want 2, 4, 0", reported)
	}
	if !r.readout.Visible() || r.readout.Text != "0 m · 612 m" {
		t.Errorf("readout %q, visible %v", r.readout.Text, r.readout.Visible())
	}
	if x := r.cursor.Position1.X; x != r.plot.X {
		t.Errorf("cursor at x=%v, want the start of the plot at %v", x, r.plot.X)
	}

	p.MouseOut()
	if p.Cursor() != -1 || reported[len(reported)-1] != -1 || r.readout.Visible() || r.dot.Visible() {
		t.Error("the cursor stayed after the pointer left")
	}
	p.SetCursor(3)
	if len(reported) != 4 || !r.dot.Visible() || r.readout.Text != "9.0 km · 980 m" {
		t.Errorf("SetCursor: reported %v, readout %q", reported, r.readout.Text)
	}
	p.SetSamples(climb[:1])
	if p.Cursor() != -1 || r.dot.Visible() {
		t.Error("new samples kept the cursor")
	}
	p.Tapped(&fyne.PointEvent{Position: at(0)})
	if len(reported) != 4 {
		t.Error("a profile of one sample reported a cursor")
	}
}

func TestDrawFillsBeneathTheLine(t *testing.T) {
	p, _ := shown(t, []Sample{{0, 0}, {1000, 100}})
	p.Color = color.NRGBA{R: 200, A: 255}
	p.Refresh()
	r := p.r
	img := r.draw(int(r.plotSize.Width+2*lineWidth), int(r.plotSize.Height+2*lineWidth))
	b := img.Bounds()
	if _, _, _, a := img.At(b.Dx()/4, b.Dy()/4).RGBA(); a != 0 {
		t.Error("painted above the line")
	}
	red, _, _, a := img.At(b.Dx()*3/4, b.Dy()*3/4).RGBA()
	if a == 0 || red == 0 {
		t.Error("left the area beneath the line empty")
	}
}

func TestFormats(t *testing.T) {
	for _, tt := range []struct {
		got, want string
	}{
		{Distance(0), "0 m"}, {Distance(999.4), "999 m"}, {Distance(12345), "12.3 km"},
		{Height(1234.6), "1235 m"}, {mark(2500, 5000), "2.5 km"}, {mark(4000, 5000), "4 km"}, {mark(200, 800), "200 m"},
	} {
		if tt.got != tt.want {
			t.Errorf("%q, want %q", tt.got, tt.want)
		}
	}
	for _, tt := range []struct {
		span, want float64
	}{{368, 200}, {12, 5}, {3, 1}, {0, 10}, {9000, 5000}} {
		if got := niceStep(tt.span, 3); got != tt.want {
			t.Errorf("step for %v: %v, want %v", tt.span, got, tt.want)
		}
	}
}
