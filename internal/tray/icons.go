// Package tray renders the system tray icons of the application.
//
// The glyphs are drawn here rather than shipped as image files: two 64 px
// monochrome shapes are less code than an asset pipeline, they cannot go
// missing from a package or a build, and drawing them keeps the muted variant
// pixel-aligned with the plain one. Nothing here reaches the network or the
// file system.
//
// A tray icon is a silhouette: only the alpha channel carries the shape. macOS
// takes it as a template image and tints it for the menu bar, so the colour is
// ignored there; Windows and Linux paint it as it is, which is why the light
// variant exists for dark panels.
package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"
)

// iconSize is the edge of the rendered square in pixels. It matches the size
// Wails' own tray icons ship at, which every platform scales down cleanly.
const iconSize = 64

// subsamples is the supersampling grid per pixel and per axis: 16 samples per
// pixel is enough to keep a 4 px stem from stair-stepping.
const subsamples = 4

// Geometry of the microphone, in fractions of the icon edge. The capsule is
// the body, the lower half of a ring is the cradle it hangs in, and a stem
// and a bar make the stand.
const (
	bodyX       = 0.5
	bodyTop     = 0.20
	bodyBottom  = 0.48
	bodyRadius  = 0.13
	cradleY     = 0.46
	cradleR     = 0.26
	cradleWidth = 0.045
	stemHalf    = 0.045
	stemTop     = 0.72
	stemBottom  = 0.88
	baseLeft    = 0.30
	baseRight   = 0.70
	baseTop     = 0.845
	baseBottom  = 0.935

	// The slash of the muted variant runs corner to corner. It is drawn over
	// a wider transparent knockout so it reads as a separate stroke rather
	// than melting into the glyph.
	slashX1     = 0.20
	slashY1     = 0.18
	slashX2     = 0.80
	slashY2     = 0.82
	slashRadius = 0.055
	slashCut    = 0.105
)

// Icon returns the dark glyph: black ink for a light panel, and the template
// image macOS tints itself. The bytes are a PNG and must not be modified.
func Icon(muted bool) []byte { return rendered()[index(false, muted)] }

// IconLight returns the white glyph, for a dark panel on Windows or Linux.
// The bytes are a PNG and must not be modified.
func IconLight(muted bool) []byte { return rendered()[index(true, muted)] }

// rendered draws all four icons once, on first use. A process that never
// shows a tray pays nothing.
var rendered = sync.OnceValue(func() [4][]byte {
	var out [4][]byte
	for _, light := range []bool{false, true} {
		for _, muted := range []bool{false, true} {
			out[index(light, muted)] = encode(draw(light, muted))
		}
	}
	return out
})

func index(light, muted bool) int {
	i := 0
	if light {
		i |= 1
	}
	if muted {
		i |= 2
	}
	return i
}

// draw rasterizes one glyph. Coverage is counted per subsample rather than
// blended per shape, so the knockout around the slash stays exact where it
// crosses the microphone.
func draw(light, muted bool) *image.NRGBA {
	ink := color.NRGBA{A: 255}
	if light {
		ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}

	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	const samples = subsamples * subsamples
	for py := 0; py < iconSize; py++ {
		for px := 0; px < iconSize; px++ {
			covered := 0
			for sy := 0; sy < subsamples; sy++ {
				for sx := 0; sx < subsamples; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/subsamples) / iconSize
					y := (float64(py) + (float64(sy)+0.5)/subsamples) / iconSize
					if inGlyph(x, y, muted) {
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			pixel := ink
			pixel.A = uint8(math.Round(float64(covered) * 255 / samples))
			img.SetNRGBA(px, py, pixel)
		}
	}
	return img
}

// inGlyph reports whether one sample point is ink.
func inGlyph(x, y float64, muted bool) bool {
	if muted {
		if inCapsule(x, y, slashX1, slashY1, slashX2, slashY2, slashRadius) {
			return true
		}
		if inCapsule(x, y, slashX1, slashY1, slashX2, slashY2, slashCut) {
			return false
		}
	}
	return inMicrophone(x, y)
}

func inMicrophone(x, y float64) bool {
	switch {
	case inCapsule(x, y, bodyX, bodyTop, bodyX, bodyBottom, bodyRadius):
		return true
	case inCradle(x, y):
		return true
	case inRect(x, y, bodyX-stemHalf, stemTop, bodyX+stemHalf, stemBottom):
		return true
	case inRect(x, y, baseLeft, baseTop, baseRight, baseBottom):
		return true
	}
	return false
}

// inCradle is the lower half of a ring: the arm the microphone hangs in.
func inCradle(x, y float64) bool {
	if y < cradleY {
		return false
	}
	d := math.Hypot(x-bodyX, y-cradleY)
	return math.Abs(d-cradleR) <= cradleWidth
}

// inCapsule reports whether the point lies within radius of the segment.
func inCapsule(x, y, x1, y1, x2, y2, radius float64) bool {
	dx, dy := x2-x1, y2-y1
	length := dx*dx + dy*dy
	t := 0.0
	if length > 0 {
		t = ((x-x1)*dx + (y-y1)*dy) / length
		t = math.Max(0, math.Min(1, t))
	}
	return math.Hypot(x-(x1+t*dx), y-(y1+t*dy)) <= radius
}

func inRect(x, y, left, top, right, bottom float64) bool {
	return x >= left && x <= right && y >= top && y <= bottom
}

// encode renders the image to PNG. The encoder is deterministic, so the same
// glyph is byte-identical on every run and on every platform.
func encode(img image.Image) []byte {
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&buf, img); err != nil {
		// The only failure mode of the PNG encoder is a failing writer, and
		// bytes.Buffer does not fail. An empty icon is still better than a
		// tray that takes the process down with it.
		return nil
	}
	return buf.Bytes()
}
