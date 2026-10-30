// Command icons draws the PWA icons and writes them into web/static.
//
// It is run by hand when the icon changes, and its output is committed — there
// is no build step in this project (ADR 0001, ADR 0011):
//
//	go run ./tools/icons
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// The icon is three ascending bars on the app's accent green: a ledger growing,
// drawn with rectangles so it needs no font and no drawing library.
var (
	background = color.RGBA{R: 0x1c, G: 0x3d, B: 0x2c, A: 0xff}
	bar        = color.RGBA{R: 0x7f, G: 0xc3, B: 0xa1, A: 0xff}
)

func main() {
	for _, size := range []int{192, 512} {
		path := filepath.Join("web", "static", "icon-"+strconv.Itoa(size)+".png")
		if err := write(path, size); err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		log.Printf("wrote %s", path)
	}
}

func write(path string, size int) error {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Rounded-square background: a full square with the corners cut, which is
	// close enough at icon sizes and keeps this to plain arithmetic.
	radius := size / 6
	for y := range size {
		for x := range size {
			if inRoundedSquare(x, y, size, radius) {
				img.Set(x, y, background)
			}
		}
	}

	// Three bars, ascending, inside a centred content box.
	pad := size / 5
	inner := size - 2*pad
	barW := inner / 4
	gap := (inner - 3*barW) / 2
	heights := []int{inner * 4 / 10, inner * 7 / 10, inner}

	for i, h := range heights {
		x0 := pad + i*(barW+gap)
		y0 := pad + inner - h
		for y := y0; y < pad+inner; y++ {
			for x := x0; x < x0+barW; x++ {
				img.Set(x, y, bar)
			}
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func inRoundedSquare(x, y, size, r int) bool {
	switch {
	case x >= r && x < size-r:
		return true
	case y >= r && y < size-r:
		return true
	}
	cx, cy := r, r
	if x >= size-r {
		cx = size - r - 1
	}
	if y >= size-r {
		cy = size - r - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}
