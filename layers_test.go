package aseprite

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"os"
	"testing"

	"github.com/askeladdk/aseprite/internal/blend"
	"github.com/askeladdk/aseprite/internal/require"
)

// ----- minimal in-memory Aseprite file builder -----

type testLayer struct {
	name    string
	flags   uint16
	typ     uint16
	level   uint16
	opacity uint8
	blend   uint16
}

type testCel struct {
	layer     int
	x, y      int
	opacity   uint8
	kind      uint16 // 0 = raw image, 1 = linked
	linkFrame uint16
	img       image.Image
	truncate  int // >0 truncates the cel payload to this many bytes
}

type testFile struct {
	w, h   uint16
	bpp    uint16
	pal    color.Palette
	layers []testLayer
	frames [][]testCel
}

func put16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
func put32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

func makeChunk(typ uint16, payload []byte) []byte {
	b := make([]byte, 6+len(payload))
	put32(b, uint32(len(b)))
	put16(b[4:], typ)
	copy(b[6:], payload)
	return b
}

func layerChunk(l testLayer) []byte {
	p := make([]byte, 18+len(l.name))
	put16(p[0:], l.flags)
	put16(p[2:], l.typ)
	put16(p[4:], l.level)
	put16(p[10:], l.blend)
	p[12] = l.opacity
	put16(p[16:], uint16(len(l.name)))
	copy(p[18:], l.name)
	return makeChunk(0x2004, p)
}

func paletteChunk(pal color.Palette) []byte {
	p := make([]byte, 20+6*len(pal))
	put32(p[0:], uint32(len(pal)))
	for i, c := range pal {
		r, g, b, a := c.RGBA()
		o := 20 + 6*i
		put16(p[o:o+2], 0) // entry flags
		p[o+2] = uint8(r >> 8)
		p[o+3] = uint8(g >> 8)
		p[o+4] = uint8(b >> 8)
		p[o+5] = uint8(a >> 8)
	}
	return makeChunk(0x2019, p)
}

func celChunk(c testCel, bpp uint16) []byte {
	hdr := make([]byte, 16)
	put16(hdr[0:], uint16(c.layer))
	put16(hdr[2:], uint16(c.x))
	put16(hdr[4:], uint16(c.y))
	hdr[6] = c.opacity
	put16(hdr[7:], c.kind)

	var payload []byte
	if c.kind == 1 {
		body := make([]byte, 2)
		put16(body, c.linkFrame)
		payload = append(hdr, body...)
	} else {
		var pix []byte
		var cw, ch int
		switch img := c.img.(type) {
		case *image.NRGBA:
			pix, cw, ch = img.Pix, img.Rect.Dx(), img.Rect.Dy()
		case *image.Paletted:
			pix, cw, ch = img.Pix, img.Rect.Dx(), img.Rect.Dy()
		default:
			panic("unsupported test image")
		}

		body := make([]byte, 4+len(pix))
		put16(body[0:], uint16(cw))
		put16(body[2:], uint16(ch))
		copy(body[4:], pix)
		payload = append(hdr, body...)
	}

	if c.truncate > 0 && c.truncate < len(payload) {
		payload = payload[:c.truncate]
	}

	return makeChunk(0x2005, payload)
}

func (tf testFile) build() []byte {
	var frameChunks [][]byte
	for fi, cels := range tf.frames {
		var chunks []byte
		if fi == 0 {
			for _, l := range tf.layers {
				chunks = append(chunks, layerChunk(l)...)
			}
			if tf.bpp == 8 {
				chunks = append(chunks, paletteChunk(tf.pal)...)
			}
		}
		for _, c := range cels {
			chunks = append(chunks, celChunk(c, tf.bpp)...)
		}

		fr := make([]byte, 16+len(chunks))
		put32(fr[0:], uint32(len(fr)))
		put16(fr[4:], 0xF1FA)
		put16(fr[8:], 100) // duration
		put32(fr[12:], uint32(tf.frameChunksCount(fi)))
		copy(fr[16:], chunks)
		frameChunks = append(frameChunks, fr)
	}

	ncolors := uint16(256)
	flags := uint16(0)
	if tf.bpp == 8 {
		ncolors = uint16(len(tf.pal))
		flags = 1
	}

	hdr := make([]byte, 128)
	put16(hdr[4:], 0xA5E0)
	put16(hdr[6:], uint16(len(tf.frames)))
	put16(hdr[8:], tf.w)
	put16(hdr[10:], tf.h)
	put16(hdr[12:], tf.bpp)
	put16(hdr[14:], flags)
	hdr[28] = 0 // transparent palette index
	put16(hdr[32:], ncolors)
	hdr[34], hdr[35] = 1, 1

	var buf bytes.Buffer
	buf.Write(hdr)
	for _, fr := range frameChunks {
		buf.Write(fr)
	}
	out := buf.Bytes()
	put32(out[0:], uint32(len(out)))
	return out
}

func (tf testFile) frameChunksCount(fi int) int {
	n := len(tf.frames[fi])
	if fi == 0 {
		n += len(tf.layers)
		if tf.bpp == 8 {
			n++
		}
	}
	return n
}

func solidNRGBA(w, h int, r, g, b, a uint8) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, a
	}
	return img
}

// ----- fixtures -----

// twoFrameThreeLayer builds the canonical fixture: 2 frames, 3 drawable
// layers (body, clothes, weapon), a linked cel and an empty frame cel.
func twoFrameThreeLayer() testFile {
	return testFile{
		w: 8, h: 8, bpp: 32,
		layers: []testLayer{
			{name: "body", flags: 1, opacity: 255, blend: 0},
			{name: "clothes", flags: 1, opacity: 128, blend: 0},
			{name: "weapon", flags: 1, opacity: 255, blend: 0},
		},
		frames: [][]testCel{
			{
				{layer: 0, x: 0, y: 0, opacity: 255, img: solidNRGBA(4, 4, 255, 0, 0, 255)},
				{layer: 1, x: 2, y: 2, opacity: 255, img: solidNRGBA(4, 4, 0, 255, 0, 255)},
				{layer: 2, x: 6, y: 6, opacity: 255, img: solidNRGBA(2, 2, 0, 0, 255, 255)},
			},
			{
				{layer: 0, kind: 1, linkFrame: 0},
				{layer: 1, x: 0, y: 0, opacity: 255, img: solidNRGBA(3, 3, 0, 255, 0, 255)},
				// weapon has no cel on frame 1: empty layer frame.
			},
		},
	}
}

func decodeWithLayers(t *testing.T, tf testFile) *Aseprite {
	t.Helper()
	spr, err := ReadWithOptions(bytes.NewReader(tf.build()), &Options{LayerAtlases: true})
	require.NoError(t, err)
	return spr
}

// recompose rebuilds the composite image solely from the per-layer atlases
// and cel metadata, mirroring buildAtlas. Matching output proves that layer
// pixels and the flattened composite correspond exactly.
func recompose(t *testing.T, spr *Aseprite) *image.NRGBA {
	t.Helper()

	frameRect := image.Rect(0, 0, spr.Frames[0].Bounds.Dx(), spr.Frames[0].Bounds.Dy())
	// Match the composite atlas bounds exactly; the frame grid may wrap onto
	// multiple rows, so a horizontal strip would be wrong.
	out := image.NewNRGBA(spr.Image.Bounds())
	transparent := &image.Uniform{color.Transparent}

	for fi := range spr.Frames {
		canvas := image.NewNRGBA(frameRect)
		dstblend := image.NewRGBA(frameRect)

		for _, l := range spr.Layers() {
			if !l.Visible || l.Reference || l.Type == GroupLayer {
				continue
			}

			lf, err := spr.LayerFrameAt(l.Index, fi)
			require.NoError(t, err)
			if lf.Empty {
				continue
			}

			atlas, err := spr.LayerAtlas(l.Index)
			require.NoError(t, err)

			// Copy the frame region out of the atlas into a fresh frame-local
			// image whose Min is the cel origin, removing any atlas offset.
			region := atlas.(interface {
				SubImage(image.Rectangle) image.Image
			}).SubImage(lf.Bounds)
			localRect := image.Rectangle{Min: lf.CelBounds.Min, Max: lf.CelBounds.Min.Add(lf.Bounds.Size())}
			sub := image.NewNRGBA(localRect)
			draw.Draw(sub, localRect, region, lf.Bounds.Min, draw.Src)
			sr := sub.Bounds()
			sp := sr.Min
			mask := &image.Uniform{color.Alpha{A: lf.Opacity}}

			// Mirror buildAtlas exactly. The blend path samples the running
			// composite (canvas) at the cel origin sp and writes the blended
			// frame into dstblend; the result is then masked on. The normal
			// path applies the combined layer/cel opacity mask directly.
			if l.BlendMode > 0 && int(l.BlendMode) < len(blend.Modes) {
				draw.Draw(dstblend, frameRect, transparent, image.Point{}, draw.Src)
				blend.Blend(dstblend, frameRect, sub, sp, canvas, sp, blend.Modes[l.BlendMode])
				draw.DrawMask(canvas, sr, dstblend, image.Point{}, mask, image.Point{}, draw.Over)
			} else {
				draw.DrawMask(canvas, sr, sub, sp, mask, image.Point{}, draw.Over)
			}
		}

		draw.Draw(out, spr.Frames[fi].Bounds, canvas, image.Point{}, draw.Src)
	}

	return out
}

func imagesEqual(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}

// pixelAt compares colors by their premultiplied components so that
// different underlying pixel formats (RGBA/NRGBA/Paletted) compare equal.
func pixelAt(t *testing.T, img image.Image, x, y int, want color.Color) {
	t.Helper()
	r1, g1, b1, a1 := img.At(x, y).RGBA()
	r2, g2, b2, a2 := want.RGBA()
	require.True(t, r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2, "pixel", x, y, "got", img.At(x, y), "want", want)
}

func testFileFromDisk(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

func TestLayerAtlasesMetadata(t *testing.T) {
	spr := decodeWithLayers(t, twoFrameThreeLayer())

	layers := spr.Layers()
	require.True(t, len(layers) == 3, "layers", len(layers))

	want := []Layer{
		{Name: "body", Index: 0, Type: NormalLayer, Visible: true, Opacity: 255, BlendMode: NormalBlend},
		{Name: "clothes", Index: 1, Type: NormalLayer, Visible: true, Opacity: 128, BlendMode: NormalBlend},
		{Name: "weapon", Index: 2, Type: NormalLayer, Visible: true, Opacity: 255, BlendMode: NormalBlend},
	}
	for i := range want {
		require.True(t, layers[i] == want[i], "layer metadata", i, layers[i])
	}

	l, err := spr.Layer(1)
	require.NoError(t, err)
	require.True(t, l.Name == "clothes", "layer by index")

	_, err = spr.Layer(-1)
	require.True(t, err == ErrLayerNotFound, "negative index")
	_, err = spr.Layer(3)
	require.True(t, err == ErrLayerNotFound, "out of range index")

	byName, err := spr.LayerByName("weapon")
	require.NoError(t, err)
	require.True(t, byName.Index == 2, "layer by name")

	// The name-based atlas entry point resolves to the same atlas.
	atlasByName, err := spr.LayerAtlasByName("weapon")
	require.NoError(t, err)
	atlasByIndex, err := spr.LayerAtlas(2)
	require.NoError(t, err)
	require.True(t, atlasByName == atlasByIndex, "name and index atlases identical")

	if _, err := spr.LayerAtlasByName("missing"); err != ErrLayerNotFound {
		t.Fatalf("missing atlas name: got %v", err)
	}

	_, err = spr.LayerByName("missing")
	require.True(t, err == ErrLayerNotFound, "missing name")
}

func TestLayerAtlasPixels(t *testing.T) {
	tf := twoFrameThreeLayer()
	spr := decodeWithLayers(t, tf)

	body, err := spr.LayerAtlas(0)
	require.NoError(t, err)

	// Body cel is a 4x4 red square at the origin of frame 0.
	lf, err := spr.LayerFrameAt(0, 0)
	require.NoError(t, err)
	require.True(t, !lf.Empty && !lf.Linked, "body frame 0 descriptor")
	require.True(t, lf.Bounds == image.Rect(0, 0, 4, 4), "body bounds", lf.Bounds)
	require.True(t, lf.CelBounds == image.Rect(0, 0, 4, 4), "body cel bounds")
	pixelAt(t, body, 1, 1, color.NRGBA{255, 0, 0, 255})
	pixelAt(t, body, 5, 5, color.NRGBA{0, 0, 0, 0})

	// Clothes cel starts at (2,2); the layer atlas keeps cel coordinates.
	clothes, err := spr.LayerAtlas(1)
	require.NoError(t, err)
	lf, err = spr.LayerFrameAt(1, 0)
	require.NoError(t, err)
	require.True(t, lf.Bounds == image.Rect(2, 2, 6, 6), "clothes bounds", lf.Bounds)
	pixelAt(t, clothes, 3, 3, color.NRGBA{0, 255, 0, 255})
	// The layer atlas stores full cel opacity; layer opacity is metadata.
	require.True(t, lf.Opacity == 128, "layer opacity", lf.Opacity)

	// Frame 1 body is a linked cel: same pixels, second frame region, linked marker.
	lfLink, err := spr.LayerFrameAt(0, 1)
	require.NoError(t, err)
	require.True(t, lfLink.Linked && !lfLink.Empty, "linked descriptor")
	require.True(t, lfLink.Bounds == image.Rect(8, 0, 12, 4), "linked bounds", lfLink.Bounds)
	pixelAt(t, body, 9, 1, color.NRGBA{255, 0, 0, 255})

	// Weapon has no cel on frame 1: explicit empty result.
	lfEmpty, err := spr.LayerFrameAt(2, 1)
	require.NoError(t, err)
	require.True(t, lfEmpty.Empty && lfEmpty.Bounds.Empty(), "empty descriptor", lfEmpty)

	// Invalid frame references have a defined error result.
	_, err = spr.LayerFrameAt(0, 2)
	require.True(t, err == ErrFrameNotFound, "frame out of range")
	_, err = spr.LayerFrameAt(9, 0)
	require.True(t, err == ErrLayerNotFound, "layer out of range")

	// Every frame descriptor covers exactly the frame grid.
	for li := range spr.Layers() {
		for fi := range spr.Frames {
			lf, err := spr.LayerFrameAt(li, fi)
			require.NoError(t, err)
			if !lf.Empty {
				require.True(t, lf.Bounds.In(spr.Frames[fi].Bounds), "bounds inside frame", li, fi)
			}
		}
	}
}

func TestLayerAtlasMatchesComposite(t *testing.T) {
	spr := decodeWithLayers(t, twoFrameThreeLayer())

	got := recompose(t, spr)
	require.True(t, imagesEqual(got, spr.Image), "recomposition from layer atlases must match composite")

	// A spot check of the flattened result: red body then 50% green clothes
	// must produce an opaque yellow-ish pixel containing both channels.
	nrgba := color.NRGBAModel.Convert(spr.Image.At(3, 3)).(color.NRGBA)
	require.True(t, nrgba.R > 0 && nrgba.G > 0 && nrgba.B == 0 && nrgba.A == 255, "blended pixel", nrgba)
}

func TestLayerAtlasesBlendModes(t *testing.T) {
	// The shipped blendtest fixture uses overlay and lighten layers.
	tf := testFileFromDisk(t, "testfiles/blendtest.aseprite")
	spr, err := ReadWithOptions(bytes.NewReader(tf), &Options{LayerAtlases: true})
	require.NoError(t, err)

	modes := []BlendMode{NormalBlend, OverlayBlend, LightenBlend}
	for i, want := range modes {
		require.True(t, spr.Layers()[i].BlendMode == want, "blend mode", i)
	}

	// Rebuilding the composite through the blend modes and per-layer data
	// must reproduce the flattened image exactly.
	got := recompose(t, spr)
	require.True(t, imagesEqual(got, spr.Image), "blend recomposition")
}

func TestLayerAtlasesPaletted(t *testing.T) {
	pal := color.Palette{
		color.Transparent,
		color.NRGBA{255, 0, 0, 255},
		color.NRGBA{0, 0, 255, 255},
	}
	img := image.NewPaletted(image.Rect(0, 0, 3, 2), pal)
	img.Pix[0] = 1
	img.Pix[1] = 2
	img.Pix[5] = 1 // last pixel red, rest transparent

	tf := testFile{
		w: 8, h: 8, bpp: 8, pal: pal,
		layers: []testLayer{{name: "p", flags: 1, opacity: 255}},
		frames: [][]testCel{
			{{layer: 0, x: 1, y: 1, opacity: 255, img: img}},
			{{layer: 0, kind: 1, linkFrame: 0}},
		},
	}
	spr := decodeWithLayers(t, tf)

	atlas, err := spr.LayerAtlas(0)
	require.NoError(t, err)
	pdst, ok := atlas.(*image.Paletted)
	require.True(t, ok, "paletted layer atlas")

	// Palette indices are preserved verbatim, including transparent index 0.
	// The cel is placed at sprite (1,1): cel pixel (x,y) lands at atlas (1+x,1+y).
	require.True(t, pdst.ColorIndexAt(1, 1) == 1, "red index", pdst.ColorIndexAt(1, 1))
	require.True(t, pdst.ColorIndexAt(2, 1) == 2, "blue index")
	require.True(t, pdst.ColorIndexAt(3, 1) == 0, "transparent index")
	require.True(t, pdst.ColorIndexAt(3, 2) == 1, "last pixel red at cel (2,1)")

	lf, err := spr.LayerFrameAt(0, 1)
	require.NoError(t, err)
	require.True(t, lf.Linked, "paletted linked frame")

	got := recompose(t, spr)
	require.True(t, imagesEqual(got, spr.Image), "paletted recomposition")
}

func TestHiddenReferenceAndGroupLayers(t *testing.T) {
	tf := testFile{
		w: 4, h: 4, bpp: 32,
		layers: []testLayer{
			{name: "group", flags: 1, typ: 1, level: 0, opacity: 255},
			{name: "child", flags: 1, typ: 0, level: 1, opacity: 255},
			{name: "hidden", flags: 0, typ: 0, level: 1, opacity: 255},
			{name: "reference", flags: 65, typ: 0, level: 1, opacity: 255},
		},
		frames: [][]testCel{
			{
				{layer: 1, x: 0, y: 0, opacity: 255, img: solidNRGBA(2, 2, 10, 20, 30, 255)},
				{layer: 2, x: 0, y: 0, opacity: 255, img: solidNRGBA(2, 2, 99, 99, 99, 255)},
				{layer: 3, x: 0, y: 0, opacity: 255, img: solidNRGBA(2, 2, 77, 77, 77, 255)},
			},
		},
	}
	spr := decodeWithLayers(t, tf)

	layers := spr.Layers()
	require.True(t, len(layers) == 4, "all layers listed")
	require.True(t, layers[0].Type == GroupLayer && layers[0].ChildLevel == 0, "group metadata")
	require.True(t, layers[1].ChildLevel == 1, "child level")
	require.True(t, !layers[2].Visible, "hidden metadata")
	require.True(t, layers[3].Reference, "reference metadata")

	// Only the visible, non-reference, non-group child layer gets an atlas.
	atlas, err := spr.LayerAtlas(1)
	require.NoError(t, err)
	pixelAt(t, atlas, 0, 0, color.NRGBA{10, 20, 30, 255})

	for _, idx := range []int{0, 2, 3} {
		_, err := spr.LayerAtlas(idx)
		require.True(t, err == ErrLayerNotFound, "no atlas for layer", idx)

		// Frame descriptors still return a well-defined empty result for
		// existing but non-drawable layers.
		lf, err := spr.LayerFrameAt(idx, 0)
		require.NoError(t, err)
		require.True(t, lf.Empty && lf.Bounds.Empty(), "empty descriptor for non-drawable layer", idx)
	}

	// The composite contains only the child layer.
	pixelAt(t, spr.Image, 0, 0, color.NRGBA{10, 20, 30, 255})
}

func TestDuplicateNamesAndEmptyLayers(t *testing.T) {
	tf := testFile{
		w: 4, h: 4, bpp: 32,
		layers: []testLayer{
			{name: "dup", flags: 1, opacity: 255},
			{name: "dup", flags: 1, opacity: 255},
		},
		frames: [][]testCel{
			{{layer: 0, x: 0, y: 0, opacity: 255, img: solidNRGBA(2, 2, 1, 2, 3, 255)}},
			{{layer: 1, x: 1, y: 1, opacity: 255, img: solidNRGBA(2, 2, 4, 5, 6, 255)}},
		},
	}
	spr := decodeWithLayers(t, tf)

	first, err := spr.LayerByName("dup")
	require.NoError(t, err)
	require.True(t, first.Index == 0, "first duplicate wins")

	all := spr.LayersByName("dup")
	require.True(t, len(all) == 2 && all[1].Index == 1, "all duplicates")

	// Layer 0 has no cel on frame 1 and layer 1 has no cel on frame 0.
	lf0, err := spr.LayerFrameAt(0, 1)
	require.NoError(t, err)
	require.True(t, lf0.Empty, "empty frame 1 layer 0")
	lf1, err := spr.LayerFrameAt(1, 0)
	require.NoError(t, err)
	require.True(t, lf1.Empty, "empty frame 0 layer 1")

	atlas0, err := spr.LayerAtlas(0)
	require.NoError(t, err)
	pixelAt(t, atlas0, 0, 0, color.NRGBA{1, 2, 3, 255})
}

func TestFrameSizeChangeAndClipping(t *testing.T) {
	// The 4x4 cel at (6,6) extends past the 8x8 frame and must be clipped.
	tf := testFile{
		w: 8, h: 8, bpp: 32,
		layers: []testLayer{{name: "big", flags: 1, opacity: 255}},
		frames: [][]testCel{
			{{layer: 0, x: 6, y: 6, opacity: 255, img: solidNRGBA(4, 4, 255, 255, 255, 255)}},
		},
	}
	spr := decodeWithLayers(t, tf)

	lf, err := spr.LayerFrameAt(0, 0)
	require.NoError(t, err)
	require.True(t, lf.CelBounds == image.Rect(6, 6, 10, 10), "full cel bounds retained", lf.CelBounds)
	require.True(t, lf.Bounds == image.Rect(6, 6, 8, 8), "atlas bounds clipped", lf.Bounds)

	atlas, err := spr.LayerAtlas(0)
	require.NoError(t, err)
	pixelAt(t, atlas, 7, 7, color.NRGBA{255, 255, 255, 255})
}

func TestInvalidAndTruncatedCels(t *testing.T) {
	// deepCopy returns an independent copy so subtests cannot corrupt each
	// other through the shared frame and cel backing arrays.
	deepCopy := func(tf testFile) testFile {
		out := tf
		out.layers = append([]testLayer(nil), tf.layers...)
		out.frames = make([][]testCel, len(tf.frames))
		for i := range tf.frames {
			out.frames[i] = append([]testCel(nil), tf.frames[i]...)
		}
		return out
	}
	base := twoFrameThreeLayer()

	t.Run("invalid layer reference", func(t *testing.T) {
		tf := deepCopy(base)
		tf.frames[0] = append(tf.frames[0], testCel{layer: 99, x: 0, y: 0, opacity: 255, img: solidNRGBA(1, 1, 1, 1, 1, 255)})
		_, err := ReadWithOptions(bytes.NewReader(tf.build()), &Options{LayerAtlases: true})
		require.True(t, err == errInvalidLayerIndex, "error", err)
	})

	t.Run("invalid linked frame", func(t *testing.T) {
		tf := deepCopy(base)
		tf.frames[1][0] = testCel{layer: 0, kind: 1, linkFrame: 5}
		_, err := ReadWithOptions(bytes.NewReader(tf.build()), &Options{LayerAtlases: true})
		require.True(t, err == errInvalidLinkedCel, "error", err)
	})

	t.Run("truncated cel header", func(t *testing.T) {
		tf := deepCopy(base)
		tf.frames[0][0] = testCel{layer: 0, x: 0, y: 0, opacity: 255, img: solidNRGBA(4, 4, 255, 0, 0, 255), truncate: 10}
		_, err := ReadWithOptions(bytes.NewReader(tf.build()), &Options{LayerAtlases: true})
		require.True(t, err == errTruncatedCel, "error", err)
	})

	t.Run("truncated cel pixels", func(t *testing.T) {
		tf := deepCopy(base)
		tf.frames[0][0] = testCel{layer: 0, x: 0, y: 0, opacity: 255, img: solidNRGBA(4, 4, 255, 0, 0, 255), truncate: 24}
		_, err := ReadWithOptions(bytes.NewReader(tf.build()), &Options{LayerAtlases: true})
		require.True(t, err == errTruncatedCel, "error", err)
	})

	t.Run("truncated layer", func(t *testing.T) {
		// Hand-craft a header + one frame whose layer chunk declares a
		// payload shorter than the fixed layer header (18 bytes).
		var buf bytes.Buffer
		hdr := make([]byte, 128)
		put16(hdr[4:], 0xA5E0)
		put16(hdr[6:], 1)
		put16(hdr[8:], 8)
		put16(hdr[10:], 8)
		put16(hdr[12:], 32)
		put16(hdr[32:], 256)
		hdr[34], hdr[35] = 1, 1

		layerPayload := make([]byte, 10)
		layerChunkRaw := makeChunk(0x2004, layerPayload)
		fr := make([]byte, 16+len(layerChunkRaw))
		put32(fr[0:], uint32(len(fr)))
		put16(fr[4:], 0xF1FA)
		put32(fr[12:], 1)
		copy(fr[16:], layerChunkRaw)

		buf.Write(hdr)
		buf.Write(fr)
		out := buf.Bytes()
		put32(out[0:], uint32(len(out)))

		_, err := ReadWithOptions(bytes.NewReader(out), &Options{LayerAtlases: true})
		require.True(t, err == errTruncatedLayer, "error", err)
	})
}

func TestLayerAtlasesDisabledByDefault(t *testing.T) {
	tf := twoFrameThreeLayer()

	spr, err := Read(bytes.NewReader(tf.build()))
	require.NoError(t, err)

	// No layer state is allocated when the feature is off.
	require.True(t, spr.Layers() == nil, "no layer metadata")
	require.True(t, spr.layerAtlases == nil, "no layer atlases")
	require.True(t, spr.layerFrames == nil, "no layer frames")
	_, err = spr.LayerAtlas(0)
	require.True(t, err == ErrLayerNotFound, "atlas unavailable")

	// Explicit zero options behave identically.
	spr2, err := ReadWithOptions(bytes.NewReader(tf.build()), &Options{})
	require.NoError(t, err)
	require.True(t, spr2.layerAtlases == nil, "zero options disables atlases")
	require.True(t, imagesEqual(spr.Image, spr2.Image), "identical composite")

	// Enabling the feature must not change the flattened composite.
	spr3 := decodeWithLayers(t, tf)
	require.True(t, imagesEqual(spr.Image, spr3.Image), "composite unchanged with atlases")
	require.True(t, len(spr3.Frames) == 2, "frame count")
}
