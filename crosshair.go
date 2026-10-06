package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

const crosshairSize float32 = 20

// newCrosshair leaves the exact target visible between its arms. Canvas lines
// let pointer input reach the map; the white outline contrasts with dark roads.
func newCrosshair() *fyne.Container {
	arms := [][2]fyne.Position{
		{{X: 0, Y: 10}, {X: 7, Y: 10}},
		{{X: 13, Y: 10}, {X: 20, Y: 10}},
		{{X: 10, Y: 0}, {X: 10, Y: 7}},
		{{X: 10, Y: 13}, {X: 10, Y: 20}},
	}
	var objects []fyne.CanvasObject
	for _, stroke := range []struct {
		color color.Color
		width float32
	}{{color.White, 4}, {color.NRGBA{R: 48, G: 48, B: 48, A: 255}, 1.5}} {
		for _, arm := range arms {
			line := canvas.NewLine(stroke.color)
			line.StrokeWidth = stroke.width
			line.Position1, line.Position2 = arm[0], arm[1]
			objects = append(objects, line)
		}
	}
	crosshair := container.NewWithoutLayout(objects...)
	crosshair.Resize(fyne.NewSize(crosshairSize, crosshairSize))
	return crosshair
}
