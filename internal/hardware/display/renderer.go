// Package display renders the verified 128x160 portrait UPS screen and drives
// an ST7735 through a separate device adapter.
package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"sync"

	"ups-pi-node/internal/hardware/control"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	PortraitWidth  = 128
	PortraitHeight = 160
)

type FontPaths struct {
	Bold     string
	Regular  string
	MonoBold string
	Mono     string
}

func DefaultFontPaths() FontPaths {
	return FontPaths{
		Bold:     "/usr/share/fonts/truetype/ubuntu/Ubuntu-B.ttf",
		Regular:  "/usr/share/fonts/truetype/ubuntu/Ubuntu-R.ttf",
		MonoBold: "/usr/share/fonts/truetype/ubuntu/UbuntuMono-B.ttf",
		Mono:     "/usr/share/fonts/truetype/ubuntu/UbuntuMono-R.ttf",
	}
}

type Renderer struct {
	header font.Face
	label  font.Face
	value  font.Face
	metric font.Face
	small  font.Face
	faces  []*opentype.Face
	mu     sync.Mutex
}

func NewRenderer(paths FontPaths) (*Renderer, error) {
	renderer := &Renderer{}
	var err error
	if renderer.header, err = renderer.loadFace(paths.Bold, 10); err != nil {
		return nil, err
	}
	if renderer.label, err = renderer.loadFace(paths.Regular, 8); err != nil {
		renderer.Close()
		return nil, err
	}
	if renderer.value, err = renderer.loadFace(paths.MonoBold, 15); err != nil {
		renderer.Close()
		return nil, err
	}
	if renderer.metric, err = renderer.loadFace(paths.Mono, 10); err != nil {
		renderer.Close()
		return nil, err
	}
	if renderer.small, err = renderer.loadFace(paths.Regular, 8); err != nil {
		renderer.Close()
		return nil, err
	}
	return renderer, nil
}

func (renderer *Renderer) loadFace(path string, size float64) (font.Face, error) {
	if path == "" {
		return basicfont.Face7x13, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return basicfont.Face7x13, nil
		}
		return nil, fmt.Errorf("read display font %s: %w", path, err)
	}
	parsed, err := opentype.Parse(contents)
	if err != nil {
		return nil, fmt.Errorf("parse display font %s: %w", path, err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: size, DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("create display font %s: %w", path, err)
	}
	typedFace, ok := face.(*opentype.Face)
	if ok {
		renderer.faces = append(renderer.faces, typedFace)
	}
	return face, nil
}

func (renderer *Renderer) Close() error {
	var firstError error
	for _, face := range renderer.faces {
		if err := face.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	renderer.faces = nil
	return firstError
}

func (renderer *Renderer) Render(snapshot control.Snapshot) *image.RGBA {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()

	canvas := image.NewRGBA(image.Rect(0, 0, PortraitWidth, PortraitHeight))
	fill(canvas, canvas.Bounds(), hex(0x020609))
	stroke(canvas, canvas.Bounds(), hex(0x31404a))
	drawText(canvas, 4, 3, "UPS NODE", renderer.header, hex(0xf1f5f9))

	acText := "AC LOST"
	acColor := hex(0xff6b6b)
	if snapshot.AC {
		acText = "AC OK"
		acColor = hex(0x78e08f)
	}
	panel(canvas, image.Rect(80, 2, 126, 18), hex(0x081016), acColor)
	drawCentered(canvas, 80, 126, 5, acText, renderer.small, acColor)

	panel(canvas, image.Rect(3, 20, 125, 89), hex(0x071016), hex(0x425466))
	drawCentered(canvas, 3, 125, 24, "BATTERY", renderer.label, hex(0x94a3b8))
	drawCentered(canvas, 3, 125, 34, fmt.Sprintf("%.2fV", snapshot.VoltageV), renderer.value, hex(0xffd43b))
	drawCentered(canvas, 3, 125, 54, fmt.Sprintf("I  %+.0fmA", snapshot.CurrentMA), renderer.metric, hex(0x67e8f9))
	panel(canvas, image.Rect(12, 69, 116, 77), hex(0x111827), hex(0x334155))
	percent := clamp(snapshot.Percent, 0, 100)
	if percent > 0 {
		barWidth := int(math.Round(103 * float64(percent) / 100))
		fill(canvas, image.Rect(12, 69, 12+max(1, barWidth), 77), stateColor(snapshot.Color))
	}
	drawCentered(
		canvas,
		3,
		125,
		78,
		fmt.Sprintf("%d%%  %s", percent, snapshot.State),
		renderer.small,
		stateColor(snapshot.Color),
	)

	panel(canvas, image.Rect(3, 92, 125, 158), hex(0x06130f), hex(0x246b5a))
	drawCentered(canvas, 3, 125, 96, "19V OUTPUT / CH2", renderer.label, hex(0x94a3b8))
	drawCentered(canvas, 3, 125, 106, fmt.Sprintf("%.2fV", snapshot.OutputVoltageV), renderer.value, hex(0x6ee7b7))
	drawCentered(canvas, 3, 125, 127, fmt.Sprintf("I  %+.0fmA", snapshot.OutputCurrentMA), renderer.metric, hex(0x67e8f9))
	drawCentered(canvas, 3, 125, 142, fmt.Sprintf("P  %+.2fW", snapshot.OutputPowerW), renderer.metric, hex(0x6ee7b7))
	return canvas
}

func EncodeRGB565(source image.Image) []byte {
	bounds := source.Bounds()
	encoded := make([]byte, 0, bounds.Dx()*bounds.Dy()*2)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := source.At(x, y).RGBA()
			value := uint16((red>>11)<<11 | (green>>10)<<5 | blue>>11)
			encoded = append(encoded, byte(value>>8), byte(value))
		}
	}
	return encoded
}

// EncodeRGB666 matches the verified luma.lcd ST7735 mode (COLMOD=0x06): three
// bytes per pixel with the six most-significant bits used by the controller.
func EncodeRGB666(source image.Image) []byte {
	bounds := source.Bounds()
	encoded := make([]byte, 0, bounds.Dx()*bounds.Dy()*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := source.At(x, y).RGBA()
			encoded = append(encoded, byte(red>>8)&0xfc, byte(green>>8)&0xfc, byte(blue>>8)&0xfc)
		}
	}
	return encoded
}

// RotatePortraitToPhysical maps the upright 128x160 portrait canvas onto the
// node's 160x128 ST7735 controller memory. The direction is intentional: the
// opposite quarter-turn makes this physical panel appear upside down.
func RotatePortraitToPhysical(source image.Image) *image.RGBA {
	bounds := source.Bounds()
	rotated := image.NewRGBA(image.Rect(0, 0, bounds.Dy(), bounds.Dx()))
	for y := 0; y < rotated.Bounds().Dy(); y++ {
		for x := 0; x < rotated.Bounds().Dx(); x++ {
			rotated.Set(x, y, source.At(bounds.Min.X+y, bounds.Min.Y+bounds.Dy()-1-x))
		}
	}
	return rotated
}

func panel(destination draw.Image, rectangle image.Rectangle, background color.Color, border color.Color) {
	fill(destination, rectangle, background)
	stroke(destination, rectangle, border)
}

func fill(destination draw.Image, rectangle image.Rectangle, value color.Color) {
	draw.Draw(destination, rectangle, image.NewUniform(value), image.Point{}, draw.Src)
}

func stroke(destination draw.Image, rectangle image.Rectangle, value color.Color) {
	if rectangle.Empty() {
		return
	}
	fill(destination, image.Rect(rectangle.Min.X, rectangle.Min.Y, rectangle.Max.X, rectangle.Min.Y+1), value)
	fill(destination, image.Rect(rectangle.Min.X, rectangle.Max.Y-1, rectangle.Max.X, rectangle.Max.Y), value)
	fill(destination, image.Rect(rectangle.Min.X, rectangle.Min.Y, rectangle.Min.X+1, rectangle.Max.Y), value)
	fill(destination, image.Rect(rectangle.Max.X-1, rectangle.Min.Y, rectangle.Max.X, rectangle.Max.Y), value)
}

func drawCentered(
	destination draw.Image,
	left int,
	right int,
	top int,
	text string,
	face font.Face,
	value color.Color,
) {
	width := font.MeasureString(face, text).Ceil()
	drawText(destination, left+(right-left-width)/2, top, text, face, value)
}

func drawText(destination draw.Image, x int, top int, text string, face font.Face, value color.Color) {
	drawer := font.Drawer{
		Dst:  destination,
		Src:  image.NewUniform(value),
		Face: face,
		Dot:  fixed.P(x, top+face.Metrics().Ascent.Ceil()),
	}
	drawer.DrawString(text)
}

func stateColor(name string) color.RGBA {
	switch name {
	case "green":
		return hex(0x78e08f)
	case "yellow":
		return hex(0xffd43b)
	case "red":
		return hex(0xff6b6b)
	default:
		return hex(0xf8fafc)
	}
}

func hex(value uint32) color.RGBA {
	return color.RGBA{R: byte(value >> 16), G: byte(value >> 8), B: byte(value), A: 0xff}
}

func clamp(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func max(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
