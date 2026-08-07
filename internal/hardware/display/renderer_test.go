package display

import (
	"image"
	"image/color"
	"testing"

	"ups-pi-node/internal/hardware/control"
)

func TestPortraitRendererFitsVerifiedPanel(t *testing.T) {
	renderer, err := NewRenderer(DefaultFontPaths())
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	frame := renderer.Render(control.Snapshot{
		VoltageV: 12.16, CurrentMA: 149.333, OutputVoltageV: 18.98,
		OutputCurrentMA: 757.333, OutputPowerW: 14.37,
		Percent: 87, State: "CHARGE", Color: "yellow", AC: true,
	})
	if frame.Bounds().Dx() != PortraitWidth || frame.Bounds().Dy() != PortraitHeight {
		t.Fatalf("frame bounds = %v", frame.Bounds())
	}
	if actual := frame.RGBAAt(13, 70); actual == (color.RGBA{R: 0x11, G: 0x18, B: 0x27, A: 0xff}) {
		t.Fatal("battery bar was not rendered")
	}
	if actual := frame.RGBAAt(3, 93); actual != (color.RGBA{R: 0x24, G: 0x6b, B: 0x5a, A: 0xff}) {
		t.Fatalf("output panel border = %#v", actual)
	}
}

func TestRGB565EncodingHasExactFrameLength(t *testing.T) {
	renderer, err := NewRenderer(FontPaths{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	frame := renderer.Render(control.Snapshot{})
	encoded := EncodeRGB565(frame)
	if len(encoded) != PortraitWidth*PortraitHeight*2 {
		t.Fatalf("encoded frame length = %d", len(encoded))
	}
}

func TestRGB666PhysicalFrameMatchesLumaLayout(t *testing.T) {
	portrait := image.NewRGBA(image.Rect(0, 0, PortraitWidth, PortraitHeight))
	portrait.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	portrait.Set(PortraitWidth-1, 0, color.RGBA{G: 0xff, A: 0xff})
	portrait.Set(0, PortraitHeight-1, color.RGBA{B: 0xff, A: 0xff})
	portrait.Set(PortraitWidth-1, PortraitHeight-1, color.RGBA{R: 0xff, G: 0xff, A: 0xff})
	physical := RotatePortraitToPhysical(portrait)
	if physical.Bounds().Dx() != 160 || physical.Bounds().Dy() != 128 {
		t.Fatalf("physical bounds = %v", physical.Bounds())
	}
	if actual := physical.RGBAAt(159, 0); actual.R != 0xff || actual.G != 0 || actual.B != 0 {
		t.Fatalf("rotated red corner = %#v", actual)
	}
	if actual := physical.RGBAAt(159, 127); actual.R != 0 || actual.G != 0xff || actual.B != 0 {
		t.Fatalf("rotated green corner = %#v", actual)
	}
	if actual := physical.RGBAAt(0, 0); actual.R != 0 || actual.G != 0 || actual.B != 0xff {
		t.Fatalf("rotated blue corner = %#v", actual)
	}
	if actual := physical.RGBAAt(0, 127); actual.R != 0xff || actual.G != 0xff || actual.B != 0 {
		t.Fatalf("rotated yellow corner = %#v", actual)
	}
	encoded := EncodeRGB666(physical)
	if len(encoded) != 160*128*3 {
		t.Fatalf("RGB666 frame length = %d", len(encoded))
	}
}
