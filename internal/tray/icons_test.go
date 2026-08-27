package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"
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

var panels = map[string]Panel{"light": PanelLight, "dark": PanelDark, "either": PanelEither}

func TestIconsAreSquarePNGs(t *testing.T) {
	t.Parallel()
	for name, panel := range panels {
		for _, state := range states {
			bounds := decode(t, Icon(panel, state)).Bounds()
			if bounds.Dx() != iconSize || bounds.Dy() != iconSize {
				t.Errorf("%s icon (state=%d) is %dx%d, want %dx%d",
					name, state, bounds.Dx(), bounds.Dy(), iconSize, iconSize)
			}
		}
	}
}

// The plain glyph is one flat ink with the shape in the alpha channel. It is
// the application's colour, not a silhouette: nothing tints it for us any
// more, so a wrong ink here ships as a wrong-coloured tray.
func TestPlainIconIsOneFlatInk(t *testing.T) {
	t.Parallel()
	inks := map[Panel]color.NRGBA{PanelLight: inkOnLight, PanelDark: inkOnDark, PanelEither: inkEither}
	for name, panel := range panels {
		img := decode(t, Icon(panel, StateOpen))
		want := inks[panel]
		var opaque, translucent int
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a == 0 {
					continue
				}
				// RGBA() is alpha-premultiplied, so compare against the ink
				// scaled by the coverage of this pixel.
				wantR := uint32(want.R) * 0x101 * a / 0xffff
				wantG := uint32(want.G) * 0x101 * a / 0xffff
				wantB := uint32(want.B) * 0x101 * a / 0xffff
				if !near(r, wantR) || !near(g, wantG) || !near(b, wantB) {
					t.Fatalf("%s icon pixel (%d,%d) = %d/%d/%d/%d, want the flat ink",
						name, x, y, r, g, b, a)
				}
				if a == 0xffff {
					opaque++
				} else {
					translucent++
				}
			}
		}
		if opaque == 0 {
			t.Errorf("%s icon has no solid ink at all", name)
		}
		// No antialiasing would mean the supersampling never ran.
		if translucent == 0 {
			t.Errorf("%s icon has no partially covered pixels", name)
		}
	}
}

func near(got, want uint32) bool {
	// One step of 8-bit alpha, widened to 16 bits, covers the rounding of the
	// premultiplication.
	const tolerance = 0x202
	if got > want {
		return got-want <= tolerance
	}
	return want-got <= tolerance
}

// The eyes are holes, not paint. If they were painted in the tile colour the
// mark would only work on that one tile, and on a tray panel the face would
// fill in and the creature would lose its eyes entirely.
func TestEyesAreHoles(t *testing.T) {
	t.Parallel()
	img := decode(t, Icon(PanelLight, StateOpen))
	at := func(fx, fy float64) uint32 {
		_, _, _, a := img.At(int(fx*iconSize), int(fy*iconSize)).RGBA()
		return a
	}
	for _, eye := range []float64{bodyCX - eyeDX, bodyCX + eyeDX} {
		if a := at(eye, eyeY); a != 0 {
			t.Errorf("the eye at %.2f carries ink (alpha %d), want a hole", eye, a)
		}
	}
	// Between the eyes is body, so the holes above are holes in something.
	if a := at(bodyCX, eyeY); a != 0xffff {
		t.Errorf("the face between the eyes has alpha %d, want solid ink", a)
	}
}

// The wave is the whole idea of the mark, and the size that decides whether it
// survives is the smallest one a panel paints. What kills it there is not its
// height but its pitch: below two device pixels per half-cycle, neighbouring
// crests and troughs land in the same pixel column and no rendering can tell
// them apart. That floor is what this checks, on the numbers, because a
// rendering cannot tell a wave from noise.
func TestTheWaveIsCoarseEnoughForTheSmallestPanel(t *testing.T) {
	t.Parallel()
	const (
		small        = 16.0
		minHalfCycle = 2.0
	)
	halfCycle := (2 * bodyRX) / (2 * waveCycles) * small
	if halfCycle < minHalfCycle {
		t.Errorf("a half-cycle is %.2f px at %.0fpx, want at least %.0f: %.1f cycles is too fine to read",
			halfCycle, small, minHalfCycle, waveCycles)
	}
}

// And it has to actually be drawn: a wave with the right pitch still has to
// leave the lower edge curved rather than flat.
func TestTheLowerEdgeIsNotAStraightLine(t *testing.T) {
	t.Parallel()
	const small = 16
	img := Render(small, inkOnLight, StateOpen)

	lowest := make([]int, 0, small)
	for x := range small {
		bottom := -1
		for y := range small {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				bottom = y
			}
		}
		if bottom >= 0 {
			lowest = append(lowest, bottom)
		}
	}
	if len(lowest) == 0 {
		t.Fatal("nothing was drawn at all")
	}
	low, high := lowest[0], lowest[0]
	for _, v := range lowest {
		low = min(low, v)
		high = max(high, v)
	}
	if high-low < 2 {
		t.Errorf("the lower edge spans %d pixels at %dpx, want at least 2: the wave has flattened into a line",
			high-low, small)
	}
}

// Deafened takes the wave away, and that is the whole signal: the wave is the
// sound in this mark. Where a tooltip could carry the state - Windows - this is
// redundant; on macOS and Linux the tooltip is an empty function and this is
// the only thing that says it at all.
func TestTheDeafenedLowerEdgeIsFlat(t *testing.T) {
	t.Parallel()
	// Measured on the outline itself, not on inked pixels or on the drawn
	// layer: the slash runs to the corner well below the body, and the
	// transparent knockout around it cuts the body away in the columns it
	// crosses. Neither has anything to do with where the lower edge is.
	const steps = 400
	bottom := func(state State) (low, high float64) {
		low, high = 1, 0
		for i := range steps {
			x := (bodyCX - bodyRX) + 2*bodyRX*(float64(i)+0.5)/steps
			edge := 0.0
			for j := range steps {
				y := float64(j) / steps
				if inCreature(x, y, state) {
					edge = y
				}
			}
			low = math.Min(low, edge)
			high = math.Max(high, edge)
		}
		return low, high
	}

	low, high := bottom(StateDeafened)
	if spread := high - low; spread > 1.0/steps {
		t.Errorf("the deafened lower edge varies by %.4f, want it flat: the wave is still there", spread)
	}
	// And the open one is not flat, or the two would be saying the same thing.
	openLow, openHigh := bottom(StateOpen)
	if spread := openHigh - openLow; spread < waveAmp {
		t.Errorf("the open lower edge varies by %.4f, want about %.2f", spread, 2*waveAmp)
	}
	if bytes.Equal(Icon(PanelLight, StateMuted), Icon(PanelLight, StateDeafened)) {
		t.Error("the muted and deafened glyphs are the same image")
	}
}

// The muted variant has to be recognisably different, or the tray would report
// an open microphone while it is closed - and the slash carries its own colour,
// so the state reads without comparing shapes.
func TestMutedIconIsSlashedInTheDangerInk(t *testing.T) {
	t.Parallel()
	plain, muted := decode(t, Icon(PanelLight, StateOpen)), decode(t, Icon(PanelLight, StateMuted))

	differing, danger := 0, 0
	for y := range iconSize {
		for x := range iconSize {
			_, _, _, a := plain.At(x, y).RGBA()
			r, g, b, ma := muted.At(x, y).RGBA()
			if a != ma {
				differing++
			}
			if ma != 0xffff {
				continue
			}
			if near(r, uint32(inkSlash.R)*0x101) && near(g, uint32(inkSlash.G)*0x101) && near(b, uint32(inkSlash.B)*0x101) {
				danger++
			}
		}
	}
	if differing < iconSize*2 {
		t.Errorf("muted and plain icons differ in %d pixels, too few to tell apart", differing)
	}
	if danger < iconSize {
		t.Errorf("the muted icon has %d pixels of the danger ink, want a visible slash", danger)
	}
}

// The ink must not touch the edge: a tray scales the image down and a glyph
// bleeding into the border loses its shape.
func TestIconsKeepAMargin(t *testing.T) {
	t.Parallel()
	img := decode(t, Icon(PanelLight, StateMuted))
	for i := range iconSize {
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
	first, second := Icon(PanelLight, StateMuted), Icon(PanelLight, StateMuted)
	if !bytes.Equal(first, second) {
		t.Fatal("Icon rendered two different images")
	}
	if len(first) == 0 {
		t.Fatal("the icon is empty")
	}
	seen := make(map[string]string, len(panels))
	for name, panel := range panels {
		key := string(Icon(panel, StateOpen))
		if other, ok := seen[key]; ok {
			t.Errorf("the %s and %s panels got the same image", name, other)
		}
		seen[key] = name
	}
}

// MarkBounds is what places the mark on the application icon. If it drifted
// from the drawing the mark would sit off-centre in the tile, so it is checked
// against the rendering rather than trusted.
func TestMarkBoundsBoundTheDrawing(t *testing.T) {
	t.Parallel()
	const size = 256
	img := Render(size, inkOnLight, StateOpen)
	left, top, right, bottom := MarkBounds()

	inked := func(x, y int) bool {
		_, _, _, a := img.At(x, y).RGBA()
		return a > 0
	}
	// Nothing outside the box, allowing the one pixel antialiasing spreads to.
	for y := range size {
		for x := range size {
			if !inked(x, y) {
				continue
			}
			fx, fy := float64(x)/size, float64(y)/size
			const slack = 1.5 / size
			if fx < left-slack || fx > right+slack || fy < top-slack || fy > bottom+slack {
				t.Fatalf("ink at (%.3f, %.3f) lies outside the bounds %.2f..%.2f x %.2f..%.2f",
					fx, fy, left, right, top, bottom)
			}
		}
	}
	// And the box is tight: each edge has ink within a pixel or two of it.
	for _, edge := range []struct {
		name string
		hit  func() bool
	}{
		{"left", func() bool { return columnInked(img, size, int(left*size)+1) }},
		{"right", func() bool { return columnInked(img, size, int(right*size)-1) }},
		{"top", func() bool { return rowInked(img, size, int(top*size)+1) }},
		{"bottom", func() bool { return rowInked(img, size, int(bottom*size)-1) }},
	} {
		if !edge.hit() {
			t.Errorf("the %s bound is loose: no ink reaches it", edge.name)
		}
	}
}

func columnInked(img *image.NRGBA, size, x int) bool {
	for y := range size {
		if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
			return true
		}
	}
	return false
}

func rowInked(img *image.NRGBA, size, y int) bool {
	for x := range size {
		if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
			return true
		}
	}
	return false
}

// The vector and the raster have to be one drawing, which is the whole reason
// MarkPath is generated instead of hand-kept. Every point of the emitted wave
// is checked against the same function the rasterizer samples.
func TestTheVectorWaveFollowsTheRasterWave(t *testing.T) {
	t.Parallel()
	const canvas = 1024.0
	path := MarkPath(canvas)

	lines := regexp.MustCompile(`L(-?[\d.]+) (-?[\d.]+)`).FindAllStringSubmatch(path, -1)
	if len(lines) < 100 {
		t.Fatalf("the path has %d wave points, want the full polyline", len(lines))
	}
	left, _, right, _ := MarkBounds()
	for _, m := range lines {
		x, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatalf("unparsable x %q: %v", m[1], err)
		}
		y, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			t.Fatalf("unparsable y %q: %v", m[2], err)
		}
		fx, fy := x/canvas, y/canvas
		if fx < left-1e-6 || fx > right+1e-6 {
			t.Fatalf("wave point x=%.4f is outside %.2f..%.2f", fx, left, right)
		}
		// One decimal on this canvas is the whole error budget.
		if diff := math.Abs(fy - waveY(fx, StateOpen)); diff > 1e-3 {
			t.Fatalf("wave point at x=%.4f has y=%.4f, the drawing says %.4f", fx, fy, waveY(fx, StateOpen))
		}
	}

	// And the eyes are there as their own subpaths, or the even-odd rule has
	// nothing to punch holes with.
	if got := strings.Count(path, "a"); got != 4 {
		t.Errorf("the path has %d arc segments, want 4: two per eye", got)
	}
}
