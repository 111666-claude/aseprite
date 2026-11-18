package aseprite

import (
	"bytes"
	"image"
	"testing"
)

func TestLayerAtlasesGrayscale(t *testing.T) {
	raw := testFileFromDisk(t, "testfiles/slime_grayscale.aseprite")
	spr, err := ReadWithOptions(bytes.NewReader(raw), &Options{LayerAtlases: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(spr.Layers()) != 2 {
		t.Fatalf("layers=%d", len(spr.Layers()))
	}

	// The "base" layer is hidden in this fixture: no atlas, but still listed.
	if spr.Layers()[0].Visible {
		t.Fatal("base should be hidden")
	}
	if _, err := spr.LayerAtlas(0); err != ErrLayerNotFound {
		t.Fatal("hidden layer must not have an atlas")
	}

	// The visible "stretch" layer uses the 16bpp grayscale->NRGBA cel path.
	atlas, err := spr.LayerAtlas(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := atlas.(*image.NRGBA); !ok {
		t.Fatalf("atlas type %T, want *image.NRGBA", atlas)
	}
	for fi := range spr.Frames {
		if _, err := spr.LayerFrameAt(1, fi); err != nil {
			t.Fatalf("frame descriptor %d: %v", fi, err)
		}
	}

	if !imagesEqual(recompose(t, spr), spr.Image) {
		t.Fatal("grayscale recomposition mismatch")
	}
}
