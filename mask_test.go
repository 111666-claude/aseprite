package aseprite

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/askeladdk/aseprite/internal/require"
)

// binWriter is a little helper to build binary fixture data.
type binWriter struct {
	bytes.Buffer
}

func (w *binWriter) u16(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.Write(b[:])
}

func (w *binWriter) u32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.Write(b[:])
}

func makeChunk(typ uint16, body []byte) []byte {
	var b binWriter
	b.u32(uint32(6 + len(body)))
	b.u16(typ)
	b.Write(body)
	return b.Bytes()
}

func makeLayerChunk(flags uint16, name string) []byte {
	var b binWriter
	b.u16(flags)             // flags
	b.u16(0)                 // type: normal
	b.u16(0)                 // child level
	b.u16(0)                 // default width (ignored)
	b.u16(0)                 // default height (ignored)
	b.u16(0)                 // blend mode: normal
	b.WriteByte(255)         // opacity
	b.Write([]byte{0, 0, 0}) // reserved
	b.u16(uint16(len(name))) // name length
	b.WriteString(name)      // name
	return makeChunk(0x2004, b.Bytes())
}

func makeRawCelChunk(layer uint16, w, h int, pix []byte) []byte {
	var b binWriter
	b.u16(layer)                   // layer index
	b.u16(0)                       // x
	b.u16(0)                       // y
	b.WriteByte(255)               // opacity
	b.u16(0)                       // cel type: raw image
	b.u16(0)                       // z-index
	b.Write([]byte{0, 0, 0, 0, 0}) // reserved
	b.u16(uint16(w))               // width
	b.u16(uint16(h))               // height
	b.Write(pix)                   // RGBA pixels
	return makeChunk(0x2005, b.Bytes())
}

func makeLinkedCelChunk(layer, frame uint16) []byte {
	var b binWriter
	b.u16(layer)                   // layer index
	b.u16(0)                       // x
	b.u16(0)                       // y
	b.WriteByte(255)               // opacity
	b.u16(1)                       // cel type: linked
	b.u16(0)                       // z-index
	b.Write([]byte{0, 0, 0, 0, 0}) // reserved
	b.u16(frame)                   // linked frame
	return makeChunk(0x2005, b.Bytes())
}

func makeFrame(chunks ...[]byte) []byte {
	var body binWriter
	for _, c := range chunks {
		body.Write(c)
	}

	var b binWriter
	b.u32(uint32(16 + body.Len())) // frame size
	b.u16(0xF1FA)                  // magic
	b.u16(uint16(len(chunks)))     // old chunk count
	b.u16(100)                     // duration in ms
	b.u16(0)                       // reserved
	b.u32(uint32(len(chunks)))     // new chunk count
	b.Write(body.Bytes())
	return b.Bytes()
}

// maskTestFile builds a fixed 8x8, 32 bpp, three-frame sprite:
//
//   - Frame 0: a raw cel on a visible layer holding a 4x3 block at
//     (2,1)-(5,3) with a hole at (3,2), an isolated pixel at (6,6),
//     a pixel with alpha 128 at (0,7) and a pixel with alpha 64 at
//     (1,7). A second, invisible layer holds opaque pixels at (0,0)
//     and (7,0) that must not show up in any mask.
//   - Frame 1: an empty frame without any chunks.
//   - Frame 2: a linked cel pointing to frame 0.
func maskTestFile() []byte {
	setpix := func(pix []byte, x, y int, r, g, b, a byte) {
		i := (y*8 + x) * 4
		pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, a
	}

	visible := make([]byte, 8*8*4)
	for y := 1; y <= 3; y++ {
		for x := 2; x <= 5; x++ {
			if x == 3 && y == 2 {
				continue // hole
			}
			setpix(visible, x, y, 255, 0, 0, 255)
		}
	}
	setpix(visible, 6, 6, 0, 255, 0, 255) // isolated pixel
	setpix(visible, 0, 7, 0, 0, 255, 128) // semi-transparent
	setpix(visible, 1, 7, 0, 0, 255, 64)  // semi-transparent

	hidden := make([]byte, 8*8*4)
	setpix(hidden, 0, 0, 255, 255, 255, 255)
	setpix(hidden, 7, 0, 255, 255, 255, 255)

	var f binWriter
	f.u32(0)       // file size, patched below
	f.u16(0xA5E0)  // magic
	f.u16(3)       // frames
	f.u16(8)       // width
	f.u16(8)       // height
	f.u16(32)      // color depth
	f.u32(0)       // flags
	f.u16(100)     // speed (deprecated)
	f.u32(0)       // reserved
	f.u32(0)       // reserved
	f.WriteByte(0) // transparent palette index
	f.Write([]byte{0, 0, 0})
	f.u16(1)       // number of colors
	f.WriteByte(1) // pixel width
	f.WriteByte(1) // pixel height
	f.Write(make([]byte, 128-f.Len()))

	f.Write(makeFrame(
		makeLayerChunk(1, "visible"),
		makeLayerChunk(0, "hidden"),
		makeRawCelChunk(0, 8, 8, visible),
		makeRawCelChunk(1, 8, 8, hidden),
	))
	f.Write(makeFrame()) // empty frame
	f.Write(makeFrame(makeLinkedCelChunk(0, 0)))

	raw := f.Bytes()
	binary.LittleEndian.PutUint32(raw, uint32(len(raw)))
	return raw
}

func readMaskTestSprite(t *testing.T) *Aseprite {
	t.Helper()
	spr, err := Read(bytes.NewReader(maskTestFile()))
	require.NoError(t, err)
	require.True(t, len(spr.Frames) == 3, "frames", len(spr.Frames))
	return spr
}

func equalPoints(a, b []image.Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// blockContour is the expected outer contour of the 4x3 block with a
// hole at (3,2): the hole is not part of the outer contour, and the
// isolated pixel at (6,6) is a separate component.
var blockContour = []image.Point{
	{2, 1}, {3, 1}, {4, 1}, {5, 1},
	{5, 2},
	{5, 3}, {4, 3}, {3, 3}, {2, 3},
	{2, 2},
}

func TestFrameMask(t *testing.T) {
	spr := readMaskTestSprite(t)

	// The mask computation must not modify the atlas.
	rgba, ok := spr.Image.(*image.RGBA)
	require.True(t, ok, "atlas type")
	before := make([]byte, len(rgba.Pix))
	copy(before, rgba.Pix)

	t.Run("threshold_zero", func(t *testing.T) {
		m := spr.FrameMask(0, 0)
		require.True(t, m.Bounds == image.Rect(0, 1, 7, 8), "bounds", m.Bounds)

		// block, hole, isolated and semi-transparent pixels
		require.True(t, m.Opaque(2, 1), "block pixel")
		require.True(t, m.Opaque(5, 3), "block pixel")
		require.True(t, !m.Opaque(3, 2), "hole")
		require.True(t, m.Opaque(6, 6), "isolated pixel")
		require.True(t, m.Opaque(0, 7), "alpha 128")
		require.True(t, m.Opaque(1, 7), "alpha 64")

		// pixels of the invisible layer are not in the mask
		require.True(t, !m.Opaque(0, 0), "hidden layer")
		require.True(t, !m.Opaque(7, 0), "hidden layer")

		// outside the mask bounds
		require.True(t, !m.Opaque(-1, -1), "outside")
		require.True(t, !m.Opaque(7, 7), "outside")

		require.True(t, equalPoints(m.Contour(), blockContour), "contour", m.Contour())
	})

	t.Run("threshold_boundaries", func(t *testing.T) {
		// a pixel is solid iff alpha > threshold
		require.True(t, spr.FrameMask(0, 63).Opaque(1, 7), "alpha 64 > 63")
		require.True(t, !spr.FrameMask(0, 64).Opaque(1, 7), "alpha 64 == 64")
		require.True(t, spr.FrameMask(0, 127).Opaque(0, 7), "alpha 128 > 127")
		require.True(t, !spr.FrameMask(0, 128).Opaque(0, 7), "alpha 128 == 128")

		// only fully opaque pixels pass a high threshold
		m := spr.FrameMask(0, 200)
		require.True(t, m.Bounds == image.Rect(2, 1, 7, 7), "bounds", m.Bounds)
		require.True(t, m.Opaque(6, 6), "isolated pixel")
		require.True(t, !m.Opaque(0, 7), "alpha 128")
		require.True(t, equalPoints(m.Contour(), blockContour), "contour", m.Contour())

		// nothing passes the maximum threshold
		m = spr.FrameMask(0, 255)
		require.True(t, m.Bounds.Empty(), "bounds", m.Bounds)
		require.True(t, m.Pix == nil, "pix")
		require.True(t, m.Contour() == nil, "contour")
	})

	t.Run("empty_frame", func(t *testing.T) {
		m := spr.FrameMask(1, 0)
		require.True(t, m.Bounds.Empty(), "bounds", m.Bounds)
		require.True(t, m.Pix == nil, "pix")
		require.True(t, !m.Opaque(0, 0), "opaque")
		require.True(t, m.Contour() == nil, "contour")
	})

	t.Run("linked_cel", func(t *testing.T) {
		m := spr.FrameMask(2, 0)
		require.True(t, m.Bounds == image.Rect(0, 1, 7, 8), "bounds", m.Bounds)
		require.True(t, m.Opaque(2, 1), "block pixel")
		require.True(t, !m.Opaque(3, 2), "hole")
		require.True(t, m.Opaque(6, 6), "isolated pixel")
		require.True(t, equalPoints(m.Contour(), blockContour), "contour", m.Contour())
	})

	t.Run("out_of_range", func(t *testing.T) {
		for _, n := range []int{-1, 3, 100} {
			m := spr.FrameMask(n, 0)
			require.True(t, m.Bounds.Empty(), "bounds", n)
			require.True(t, m.Pix == nil, "pix", n)
			require.True(t, m.Contour() == nil, "contour", n)
		}
	})

	require.True(t, bytes.Equal(before, rgba.Pix), "atlas modified")
}

func TestMaskFromImage(t *testing.T) {
	t.Run("offset_origin", func(t *testing.T) {
		// mask coordinates are relative to the given rectangle
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		img.SetNRGBA(11, 12, color.NRGBA{A: 255})

		m := MaskFromImage(img, image.Rect(10, 10, 16, 16), 0)
		require.True(t, m.Bounds == image.Rect(1, 2, 2, 3), "bounds", m.Bounds)
		require.True(t, m.Opaque(1, 2), "opaque")
		require.True(t, !m.Opaque(0, 0), "opaque")
		require.True(t, equalPoints(m.Contour(), []image.Point{{1, 2}}), "contour", m.Contour())
	})

	t.Run("ring_with_hole", func(t *testing.T) {
		// a 3x3 ring: the hole stays inside Bounds but is not solid
		img := image.NewNRGBA(image.Rect(0, 0, 3, 3))
		for y := 0; y < 3; y++ {
			for x := 0; x < 3; x++ {
				if x == 1 && y == 1 {
					continue
				}
				img.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}

		m := MaskFromImage(img, img.Bounds(), 0)
		require.True(t, m.Bounds == image.Rect(0, 0, 3, 3), "bounds", m.Bounds)
		require.True(t, !m.Opaque(1, 1), "hole")

		contour := m.Contour()
		require.True(t, len(contour) == 8, "contour", contour)
		for _, p := range contour {
			require.True(t, p != image.Pt(1, 1), "hole in contour")
		}
	})

	t.Run("large_frame", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 4096, 4096))
		img.SetNRGBA(10, 20, color.NRGBA{A: 255})
		img.SetNRGBA(4000, 3000, color.NRGBA{A: 255})

		m := MaskFromImage(img, img.Bounds(), 0)
		require.True(t, m.Bounds == image.Rect(10, 20, 4001, 3001), "bounds", m.Bounds)
		require.True(t, m.Opaque(10, 20), "opaque")
		require.True(t, m.Opaque(4000, 3000), "opaque")
		require.True(t, !m.Opaque(2000, 2000), "gap")
		// the contour covers only the first component: the isolated pixel
		require.True(t, equalPoints(m.Contour(), []image.Point{{10, 20}}), "contour", m.Contour())
	})

	t.Run("no_intersection", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
		m := MaskFromImage(img, image.Rect(100, 100, 200, 200), 0)
		require.True(t, m.Bounds.Empty(), "bounds", m.Bounds)
		require.True(t, m.Pix == nil, "pix")
		require.True(t, m.Contour() == nil, "contour")
	})
}
