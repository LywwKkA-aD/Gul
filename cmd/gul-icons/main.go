// Command gul-icons writes the application's icon assets from the same mark
// the tray draws (internal/tray), so the icon in the dock and the glyph in the
// menu bar are one drawing and cannot drift apart.
//
// It is a developer tool, not something a release ships: run it when the mark
// changes, review the diff, and commit the files. What it writes is the input
// to `task generate:icons`, which turns appicon.png into the platform
// containers (.ico, .icns) - those are generated, not written here.
//
//	go run ./cmd/gul-icons            # writes into ./build
//	go run ./cmd/gul-icons -check     # fails if the files are out of date
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/LywwKkA-aD/Gul/internal/tray"
)

const (
	// appIconSize is what every platform container is resized from, and
	// dmgIconSize is the icon Finder shows on the disk image itself. Both
	// match the files Wails shipped, so nothing downstream has to change.
	appIconSize = 1024
	dmgIconSize = 1254

	// markScale leaves the tile a margin. The mark is a wide shape; filling
	// the tile edge to edge would leave it looking cramped next to icons that
	// keep the conventional inset.
	markScale = 0.82

	// tileExponent shapes the tile: a superellipse rather than a rounded
	// rectangle, which is the corner every current desktop draws.
	tileExponent = 5.0

	// subsamples is the supersampling grid per axis for the tile edge, which
	// is the only thing this file rasterizes itself.
	subsamples = 4

	// markAsset is the file name inside the Icon Composer bundle, named in
	// icon.json by that name and no other.
	markAsset = "gul-mark.svg"
)

// The tile is the accent; the mark on it is white, and its eyes are holes, so
// the tile shows through them without anything being painted twice.
var (
	tileInk = color.NRGBA{R: 0x2F, G: 0x52, B: 0xDE, A: 0xFF}
	markInk = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
)

func main() {
	check := flag.Bool("check", false, "report whether the assets on disk match, and write nothing")
	dir := flag.String("dir", "build", "the build directory to write into")
	flag.Parse()

	if err := run(*dir, *check); err != nil {
		fmt.Fprintln(os.Stderr, "gul-icons:", err)
		os.Exit(1)
	}
}

type asset struct {
	path    string
	content []byte
}

func run(dir string, check bool) error {
	appIcon, err := encodePNG(icon(appIconSize))
	if err != nil {
		return err
	}
	dmgIcon, err := encodePNG(icon(dmgIconSize))
	if err != nil {
		return err
	}
	assets := []asset{
		{filepath.Join(dir, "appicon.png"), appIcon},
		{filepath.Join(dir, "darwin", "dmg-file-icon.png"), dmgIcon},
		{filepath.Join(dir, "appicon.icon", "Assets", markAsset), []byte(markSVG())},
		{filepath.Join(dir, "appicon.icon", "icon.json"), []byte(iconComposer())},
	}

	var stale []string
	for _, a := range assets {
		current, err := os.ReadFile(a.path)
		switch {
		case err == nil && bytes.Equal(current, a.content):
			continue
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("read %s: %w", a.path, err)
		}
		stale = append(stale, a.path)
		if check {
			continue
		}
		if err := os.WriteFile(a.path, a.content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", a.path, err)
		}
		fmt.Println("wrote", a.path)
	}

	if check && len(stale) > 0 {
		return fmt.Errorf("out of date, re-run without -check: %s", strings.Join(stale, ", "))
	}
	if len(stale) == 0 {
		fmt.Println("icon assets are up to date")
	}
	return nil
}

// icon composes one square: the tile, then the mark centred on it. The mark is
// drawn by the tray package, so this file owns the tile and the placement and
// nothing about the shape itself.
func icon(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	drawTile(img)

	markSize := int(math.Round(float64(size) * markScale))
	mark := tray.Render(markSize, markInk, tray.StateOpen)

	// Centre the mark's own bounds, not its canvas: the shape sits high in the
	// square it is drawn in, and centring the canvas would leave it looking
	// dropped.
	left, top, right, bottom := tray.MarkBounds()
	offsetX := float64(size)/2 - (left+right)/2*float64(markSize)
	offsetY := float64(size)/2 - (top+bottom)/2*float64(markSize)
	at := image.Pt(int(math.Round(offsetX)), int(math.Round(offsetY)))

	draw.Draw(img, mark.Bounds().Add(at), mark, image.Point{}, draw.Over)
	return img
}

// drawTile fills the superellipse, antialiased at its edge.
func drawTile(img *image.NRGBA) {
	size := img.Bounds().Dx()
	const samples = subsamples * subsamples
	for py := range size {
		for px := range size {
			covered := 0
			for sy := range subsamples {
				for sx := range subsamples {
					x := (float64(px) + (float64(sx)+0.5)/subsamples) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/subsamples) / float64(size)
					if inTile(x, y) {
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			pixel := tileInk
			pixel.A = uint8(math.Round(float64(covered) * 255 / samples))
			img.SetNRGBA(px, py, pixel)
		}
	}
}

func inTile(x, y float64) bool {
	u := math.Abs(2*x - 1)
	v := math.Abs(2*y - 1)
	return math.Pow(u, tileExponent)+math.Pow(v, tileExponent) <= 1
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// markSVG writes the mark as a vector, for the Icon Composer bundle macOS
// builds its asset catalogue from. It is emitted from the same geometry the
// raster path uses rather than kept as a second drawing, because two drawings
// of one mark diverge the first time either is touched.
//
// The path is shifted so the mark's own bounds sit in the middle of the
// canvas. Icon Composer centres the image it is given, and the mark is not
// centred in its natural square - the dome reaches higher than the wave
// reaches down - so without this it would ride high in the finished icon while
// the raster, which centres the bounds itself, would not.
func markSVG() string {
	const canvas = 1024
	_, top, _, bottom := tray.MarkBounds()
	shift := (0.5 - (top+bottom)/2) * canvas

	var b strings.Builder
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %d %d\" width=\"%d\" height=\"%d\">\n",
		canvas, canvas, canvas, canvas)
	b.WriteString("  <!-- Generated by cmd/gul-icons from internal/tray. Do not edit. -->\n")
	fmt.Fprintf(&b, "  <g transform=\"translate(0 %.1f)\">\n", shift)
	fmt.Fprintf(&b, "    <path fill=\"#FFFFFF\" fill-rule=\"evenodd\" d=\"%s\"/>\n", tray.MarkPath(canvas))
	b.WriteString("  </g>\n</svg>\n")
	return b.String()
}

// iconComposer writes the bundle description macOS 26 builds its asset
// catalogue from. It is generated rather than hand-kept because it carries the
// tile colour, and a colour written in two places is a colour that will
// eventually be two colours.
func iconComposer() string {
	return fmt.Sprintf(`{
  "fill" : {
    "automatic-gradient" : "srgb:%.5f,%.5f,%.5f,1.00000"
  },
  "groups" : [
    {
      "layers" : [
        {
          "image-name" : %q,
          "name" : "gul-mark",
          "position" : {
            "scale" : %.2f,
            "translation-in-points" : [
              0.0,
              0.0
            ]
          }
        }
      ],
      "shadow" : {
        "kind" : "neutral",
        "opacity" : 0.5
      },
      "specular" : true,
      "translucency" : {
        "enabled" : true,
        "value" : 0.5
      }
    }
  ],
  "supported-platforms" : {
    "circles" : [
      "watchOS"
    ],
    "squares" : "shared"
  }
}
`, float64(tileInk.R)/255, float64(tileInk.G)/255, float64(tileInk.B)/255, markAsset, markScale)
}
