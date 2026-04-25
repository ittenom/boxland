// Package main generates a placeholder 16x16 icon sprite sheet so the
// design-tool surfaces have something to render before the design team
// produces final pixel art. Run with `go run server/static/icons/gen.go`.
//
// Builds with the `iconsgen` tag so it doesn't bloat normal builds.
//go:build iconsgen
// +build iconsgen

package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
)

const (
	cell      = 16
	cols      = 8
	iconCount = 32 // current placeholder count
)

// IconNames is the canonical id list. Templ helpers (added in a later task)
// look up icons by these names. Order is meaningful — names map to the
// (col, row) cell on the sprite sheet.
var IconNames = []string{
	"home", "asset", "entity", "map", "sandbox", "settings", "search", "filter",
	"plus", "minus", "edit", "trash", "duplicate", "save", "undo", "redo",
	"play", "pause", "step", "freeze", "godmode", "spawn", "control", "release",
	"layer", "lighting", "sprite", "audio", "palette", "variant", "bake", "publish",
}

func main() {
	if len(IconNames) > iconCount {
		log.Fatalf("IconNames length (%d) exceeds sheet capacity (%d)", len(IconNames), iconCount)
	}

	rows := (len(IconNames) + cols - 1) / cols
	img := image.NewNRGBA(image.Rect(0, 0, cols*cell, rows*cell))

	// Distinct hue per icon so the placeholder is visually unambiguous in
	// dev. Real icons replace this and the helper looks the same.
	for i, name := range IconNames {
		col, row := i%cols, i/cols
		drawCell(img, col, row, color.NRGBA{
			R: uint8(40 + (i*23)%200),
			G: uint8(80 + (i*37)%160),
			B: uint8(120 + (i*53)%120),
			A: 255,
		})
		_ = name
	}

	out := "static/icons/sprites.png"
	f, err := os.Create(out)
	if err != nil {
		log.Fatalf("create %s: %v", out, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatalf("encode: %v", err)
	}
	log.Printf("wrote %d placeholder icons to %s", len(IconNames), out)
}

// drawCell paints a (cell x cell) block with a 1-pixel inner border in the
// inverted color of the fill, plus a single bright pixel in the corner
// to make placeholders unambiguously NOT real art.
func drawCell(img *image.NRGBA, col, row int, fill color.NRGBA) {
	x0, y0 := col*cell, row*cell
	for y := 0; y < cell; y++ {
		for x := 0; x < cell; x++ {
			c := fill
			// 1-pixel inset border
			if x == 1 || y == 1 || x == cell-2 || y == cell-2 {
				c = color.NRGBA{R: 255 - fill.R, G: 255 - fill.G, B: 255 - fill.B, A: 255}
			}
			img.Set(x0+x, y0+y, c)
		}
	}
	// Corner marker so devs know placeholders aren't shipping art.
	img.Set(x0+cell-2, y0+1, color.NRGBA{R: 255, G: 211, B: 74, A: 255})
}
