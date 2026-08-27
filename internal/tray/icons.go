// Package tray renders the application's mark: the tray glyphs, and the shape
// the icon generator paints onto the application icon.
//
// The mark is drawn here rather than shipped as image files: it is one closed
// shape and less code than an asset pipeline, it cannot go missing from a
// package or a build, and drawing it keeps the muted variant pixel-aligned
// with the plain one. Nothing here reaches the network or the file system.
//
// The shape is a creature whose lower edge is not an edge but a sound wave -
// the name drawn out. It carries colour rather than being a silhouette: a tray
// icon that the system tints for us would come out grey, and the mark is the
// one place the application says which application it is. That costs the
// automatic adaptation a macOS template image gives, so the ink comes as a
// pair - the accent for a light panel, the same blue lifted for a dark one -
// and the caller picks by panel. Only Windows can actually be told about both;
// see PanelEither.
package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
	"sync"
)

// iconSize is the edge of the rendered square in pixels. It matches the size
// Wails' own tray icons ship at, which every platform scales down cleanly.
const iconSize = 64

// subsamples is the supersampling grid per pixel and per axis: 16 samples per
// pixel is enough to keep the wave from stair-stepping.
const subsamples = 4

// Geometry of the mark, in fractions of the icon edge.
//
// The body is the top half of an ellipse standing on two straight sides, and
// the bottom is a sine wave that meets those sides at its own midline, so the
// silhouette closes without a seam.
const (
	bodyCX = 0.50
	bodyCY = 0.50 // where the dome ends and the straight sides begin
	bodyRX = 0.36
	bodyRY = 0.34 // dome top at 0.16

	// waveMid is the midline of the bottom wave, waveAmp its height.
	//
	// Two and a half cycles, and the half is not a rounding: a whole number of
	// cycles ends the wave on the opposite phase from where it started and the
	// mark comes out lopsided. The odd half mirrors it.
	//
	// The count is bounded from both sides. Fewer than this and the lower edge
	// reads as legs rather than as a wave, which is the one thing the mark is
	// about. More, and at the size a tray actually paints - sixteen device
	// pixels on a great many panels - neighbouring half-cycles fall in the same
	// pixel column and the wave collapses into a ragged line. At 2.5 a
	// half-cycle is a shade over two pixels there, which is the floor.
	waveMid    = 0.70
	waveAmp    = 0.08
	waveCycles = 2.5

	// The eyes are holes, not ink: they let the tile behind show through, so
	// the same shape works on the icon and on a bare panel.
	eyeR  = 0.08
	eyeY  = 0.45
	eyeDX = 0.14

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

// The inks. All three are the application accent at different lightnesses; the
// slash is the palette's danger colour, so a closed microphone reads by colour
// and not by shape alone.
var (
	inkOnLight = color.NRGBA{R: 0x2F, G: 0x52, B: 0xDE, A: 0xFF} // --accent
	inkOnDark  = color.NRGBA{R: 0x6D, G: 0x86, B: 0xE8, A: 0xFF} // lifted to survive near-black
	inkEither  = color.NRGBA{R: 0x4B, G: 0x6B, B: 0xE5, A: 0xFF} // --speak: the compromise
	inkSlash   = color.NRGBA{R: 0xCE, G: 0x4A, B: 0x55, A: 0xFF} // --danger
)

// Panel is the background the tray will paint the icon on.
type Panel uint8

const (
	// PanelLight and PanelDark are the two a platform can tell apart, and get
	// the ink that reads best on each.
	PanelLight Panel = iota
	PanelDark
	// PanelEither is for a platform that keeps one image and changes the
	// appearance underneath it. macOS and Linux are both that platform: Wails'
	// dark-mode setter on each calls the same setter as the plain one
	// (systemtray_darwin.go, systemtray_linux.go), so a second image only
	// overwrites the first. This ink is a compromise chosen to clear both
	// panels. Windows is the only one that keeps two.
	PanelEither
)

// Icon returns the glyph for one panel and mute state. The bytes are a PNG and
// must not be modified.
func Icon(panel Panel, muted bool) []byte { return rendered()[index(panel, muted)] }

// rendered draws every icon once, on first use. A process that never shows a
// tray pays nothing.
var rendered = sync.OnceValue(func() [6][]byte {
	inks := [...]color.NRGBA{PanelLight: inkOnLight, PanelDark: inkOnDark, PanelEither: inkEither}
	var out [6][]byte
	for panel, ink := range inks {
		for _, muted := range []bool{false, true} {
			out[index(Panel(panel), muted)] = encode(Render(iconSize, ink, muted))
		}
	}
	return out
})

func index(panel Panel, muted bool) int {
	i := int(panel) * 2
	if muted {
		i++
	}
	return i
}

// Render rasterizes the mark at any size, in the given ink, over a transparent
// background. The icon generator uses it to paint the mark onto a tile; the
// tray uses it directly.
//
// Coverage is counted per subsample and split between the two inks rather than
// blended per shape, so the knockout around the slash stays exact where it
// crosses the body, and the two never bleed into each other.
func Render(size int, ink color.NRGBA, muted bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const samples = subsamples * subsamples
	for py := range size {
		for px := range size {
			var body, slash int
			for sy := range subsamples {
				for sx := range subsamples {
					x := (float64(px) + (float64(sx)+0.5)/subsamples) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/subsamples) / float64(size)
					switch classify(x, y, muted) {
					case layerBody:
						body++
					case layerSlash:
						slash++
					}
				}
			}
			covered := body + slash
			if covered == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: mix(ink.R, inkSlash.R, body, slash),
				G: mix(ink.G, inkSlash.G, body, slash),
				B: mix(ink.B, inkSlash.B, body, slash),
				A: uint8(math.Round(float64(covered) * 255 / samples)),
			})
		}
	}
	return img
}

// mix averages the two inks by the share of the pixel each one covers. The
// layers are disjoint by construction, so an average is the whole compositing
// rule: nothing is ever drawn on top of anything else.
func mix(a, b uint8, weightA, weightB int) uint8 {
	total := weightA + weightB
	return uint8((int(a)*weightA + int(b)*weightB + total/2) / total)
}

type layer uint8

const (
	layerNone layer = iota
	layerBody
	layerSlash
)

// classify reports which layer one sample point belongs to.
func classify(x, y float64, muted bool) layer {
	if muted {
		if inCapsule(x, y, slashX1, slashY1, slashX2, slashY2, slashRadius) {
			return layerSlash
		}
		if inCapsule(x, y, slashX1, slashY1, slashX2, slashY2, slashCut) {
			return layerNone
		}
	}
	if inCreature(x, y) {
		return layerBody
	}
	return layerNone
}

// MarkBounds is the box the mark actually occupies, in fractions of the icon
// edge. The shape sits high in its square - the dome reaches further up than
// the wave reaches down - so anything placing the mark has to centre these
// bounds rather than the canvas.
func MarkBounds() (left, top, right, bottom float64) {
	return bodyCX - bodyRX, bodyCY - bodyRY, bodyCX + bodyRX, waveMid + waveAmp
}

// MarkPath renders the outline as an SVG path on a square canvas of the given
// edge, with the eyes as holes under the even-odd rule.
//
// It exists so the vector and the raster are one drawing: the icon generator
// needs a vector for the macOS asset catalogue, and a hand-kept second copy
// would diverge the first time either was touched. The wave is emitted as a
// dense polyline rather than fitted curves - at any size an icon is drawn, the
// difference is far below one pixel, and a polyline cannot disagree with the
// sampled shape the way a fitted curve can.
func MarkPath(canvas float64) string {
	const waveSteps = 256
	left, _, right, _ := MarkBounds()

	var b strings.Builder
	// One decimal on the canvas the generator uses is a hundredth of a pixel
	// at any size an icon is ever drawn.
	num := func(v float64) string { return strconv.FormatFloat(v*canvas, 'f', 1, 64) }
	point := func(x, y float64) { fmt.Fprintf(&b, "%s %s", num(x), num(y)) }

	// The dome: half an ellipse, left to right over the top.
	b.WriteString("M")
	point(left, bodyCY)
	fmt.Fprintf(&b, "A%s %s 0 0 1 ", num(bodyRX), num(bodyRY))
	point(right, bodyCY)
	// Down the right side, then the wave back to the left. Z closes it up the
	// left side, which is straight.
	for i := waveSteps; i >= 0; i-- {
		x := left + (right-left)*float64(i)/waveSteps
		b.WriteString("L")
		point(x, waveY(x))
	}
	b.WriteString("Z")

	// The eyes, wound as their own subpaths: even-odd turns them into holes.
	for _, cx := range []float64{bodyCX - eyeDX, bodyCX + eyeDX} {
		b.WriteString("M")
		point(cx-eyeR, eyeY)
		fmt.Fprintf(&b, "a%s %s 0 1 0 %s 0", num(eyeR), num(eyeR), num(2*eyeR))
		fmt.Fprintf(&b, "a%s %s 0 1 0 %s 0Z", num(eyeR), num(eyeR), num(-2*eyeR))
	}
	return b.String()
}

// inCreature reports whether the point is inside the body and outside the
// eyes. The eyes are subtracted here rather than painted over, so the mark has
// real holes and sits on any background.
func inCreature(x, y float64) bool {
	if x < bodyCX-bodyRX || x > bodyCX+bodyRX {
		return false
	}
	if y < bodyCY {
		dx := (x - bodyCX) / bodyRX
		dy := (y - bodyCY) / bodyRY
		if dx*dx+dy*dy > 1 {
			return false
		}
	} else if y > waveY(x) {
		return false
	}
	return !inEyes(x, y)
}

// waveY is the lower edge: a sine whose ends land on the midline, so the wave
// meets the straight sides exactly and the outline closes.
func waveY(x float64) float64 {
	phase := (x - (bodyCX - bodyRX)) / (2 * bodyRX)
	return waveMid + waveAmp*math.Sin(2*math.Pi*waveCycles*phase)
}

func inEyes(x, y float64) bool {
	return math.Hypot(x-(bodyCX-eyeDX), y-eyeY) <= eyeR ||
		math.Hypot(x-(bodyCX+eyeDX), y-eyeY) <= eyeR
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
