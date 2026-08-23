package tray

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode icon: %v", err)
	}
	return img
}

func TestIconsAreSquarePNGs(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"dark":        Icon(false),
		"dark muted":  Icon(true),
		"light":       IconLight(false),
		"light muted": IconLight(true),
	} {
		bounds := decode(t, data).Bounds()
		if bounds.Dx() != iconSize || bounds.Dy() != iconSize {
			t.Errorf("%s icon is %dx%d, want %dx%d",
				name, bounds.Dx(), bounds.Dy(), iconSize, iconSize)
		}
	}
}

// A tray icon is a silhouette: the shape lives in the alpha channel, and the
// colour is one flat ink macOS is free to replace.
func TestIconsAreFlatInkWithAlpha(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                string
		data                []byte
		wantR, wantG, wantB uint32
	}{
		{"dark", Icon(false), 0, 0, 0},
		{"light", IconLight(false), 0xffff, 0xffff, 0xffff},
	}
	for _, c := range cases {
		img := decode(t, c.data)
		var opaque, translucent int
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a == 0 {
					continue
				}
				// RGBA() is alpha-premultiplied, so compare against the ink
				// scaled by the coverage of this pixel.
				wantR, wantG, wantB := c.wantR*a/0xffff, c.wantG*a/0xffff, c.wantB*a/0xffff
				if !near(r, wantR) || !near(g, wantG) || !near(b, wantB) {
					t.Fatalf("%s icon pixel (%d,%d) = %d/%d/%d/%d, want the flat ink",
						c.name, x, y, r, g, b, a)
				}
				if a == 0xffff {
					opaque++
				} else {
					translucent++
				}
			}
		}
		if opaque == 0 {
			t.Errorf("%s icon has no solid ink at all", c.name)
		}
		// No antialiasing would mean the supersampling never ran.
		if translucent == 0 {
			t.Errorf("%s icon has no partially covered pixels", c.name)
		}
	}
}

func near(got, want uint32) bool {
	// One step of 8-bit alpha, widened to 16 bits, covers the rounding of the
	// premultiplication.
	const tolerance = 0x101
	if got > want {
		return got-want <= tolerance
	}
	return want-got <= tolerance
}

// The muted variant has to be recognisably different, or the tray would report
// an open microphone while it is closed.
func TestMutedIconDiffersFromThePlainOne(t *testing.T) {
	t.Parallel()
	plain, muted := decode(t, Icon(false)), decode(t, Icon(true))

	differing := 0
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			_, _, _, a := plain.At(x, y).RGBA()
			_, _, _, b := muted.At(x, y).RGBA()
			if a != b {
				differing++
			}
		}
	}
	if differing < iconSize*2 {
		t.Errorf("muted and plain icons differ in %d pixels, too few to tell apart", differing)
	}
}

// The ink must not touch the edge: a tray scales the image down and a glyph
// bleeding into the border loses its shape.
func TestIconsKeepAMargin(t *testing.T) {
	t.Parallel()
	img := decode(t, Icon(true))
	for i := 0; i < iconSize; i++ {
		for _, p := range []image.Point{{X: i, Y: 0}, {X: i, Y: iconSize - 1}, {X: 0, Y: i}, {X: iconSize - 1, Y: i}} {
			if _, _, _, a := img.At(p.X, p.Y).RGBA(); a != 0 {
				t.Fatalf("edge pixel (%d,%d) carries ink", p.X, p.Y)
			}
		}
	}
}

// The icons are drawn once and handed out by reference; two calls must return
// the very same bytes, not a fresh render that could differ.
func TestIconsAreRenderedOnce(t *testing.T) {
	t.Parallel()
	first, second := Icon(true), Icon(true)
	if !bytes.Equal(first, second) {
		t.Fatal("Icon(true) rendered two different images")
	}
	if len(first) == 0 {
		t.Fatal("Icon(true) is empty")
	}
	if bytes.Equal(Icon(false), IconLight(false)) {
		t.Fatal("dark and light icons are the same bytes")
	}
}
