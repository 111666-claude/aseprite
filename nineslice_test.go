package aseprite

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/askeladdk/aseprite/internal/require"
)

// bytebuf builds aseprite file structures little-endian first.
type bytebuf struct{ buf []byte }

func (b *bytebuf) u8(v byte)    { b.buf = append(b.buf, v) }
func (b *bytebuf) u16(v uint16) { b.buf = binary.LittleEndian.AppendUint16(b.buf, v) }
func (b *bytebuf) u32(v uint32) { b.buf = binary.LittleEndian.AppendUint32(b.buf, v) }
func (b *bytebuf) i32(v int32)  { b.u32(uint32(v)) }
func (b *bytebuf) zeros(n int)  { b.buf = append(b.buf, make([]byte, n)...) }
func (b *bytebuf) str(s string) { b.u16(uint16(len(s))); b.buf = append(b.buf, s...) }

func makeChunk(typ uint16, data []byte) []byte {
	var b bytebuf
	b.u32(uint32(6 + len(data)))
	b.u16(typ)
	b.buf = append(b.buf, data...)
	return b.buf
}

func layerChunk() []byte {
	var b bytebuf
	b.u16(1)   // flags: visible
	b.u16(0)   // type: normal
	b.u16(0)   // child level
	b.u16(16)  // default width
	b.u16(16)  // default height
	b.u16(0)   // blend mode: normal
	b.u8(255)  // opacity
	b.zeros(3) // reserved
	b.str("Layer 1")
	return makeChunk(0x2004, b.buf)
}

func celChunk(w, h int, pix []byte) []byte {
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write(pix)
	_ = zw.Close()

	var b bytebuf
	b.u16(0)   // layer index
	b.u16(0)   // x
	b.u16(0)   // y
	b.u8(255)  // opacity
	b.u16(2)   // cel type: compressed image
	b.u16(0)   // z-index
	b.zeros(5) // reserved
	b.u16(uint16(w))
	b.u16(uint16(h))
	b.buf = append(b.buf, z.Bytes()...)
	return makeChunk(0x2005, b.buf)
}

type sliceKey struct {
	frame  uint32
	x, y   int32
	w, h   uint32
	cx, cy int32
	cw, ch uint32
	px, py int32
}

func sliceChunk(name string, flags uint32, keys []sliceKey) []byte {
	var b bytebuf
	b.u32(uint32(len(keys)))
	b.u32(flags)
	b.u32(0) // reserved
	b.str(name)
	for _, k := range keys {
		b.u32(k.frame)
		b.i32(k.x)
		b.i32(k.y)
		b.u32(k.w)
		b.u32(k.h)
		if flags&1 != 0 {
			b.i32(k.cx)
			b.i32(k.cy)
			b.u32(k.cw)
			b.u32(k.ch)
		}
		if flags&2 != 0 {
			b.i32(k.px)
			b.i32(k.py)
		}
	}
	return makeChunk(0x2022, b.buf)
}

func makeFrame(duration uint16, chunks ...[]byte) []byte {
	var body []byte
	for _, c := range chunks {
		body = append(body, c...)
	}
	var b bytebuf
	b.u32(uint32(16 + len(body)))
	b.u16(0xF1FA) // frame magic
	b.u16(uint16(len(chunks)))
	b.u16(duration)
	b.zeros(2) // reserved
	b.u32(uint32(len(chunks)))
	b.buf = append(b.buf, body...)
	return b.buf
}

func makeHeader(w, h, frames int, filesize uint32) []byte {
	hdr := make([]byte, 128)
	binary.LittleEndian.PutUint32(hdr[0:], filesize)
	binary.LittleEndian.PutUint16(hdr[4:], 0xA5E0) // magic
	binary.LittleEndian.PutUint16(hdr[6:], uint16(frames))
	binary.LittleEndian.PutUint16(hdr[8:], uint16(w))
	binary.LittleEndian.PutUint16(hdr[10:], uint16(h))
	binary.LittleEndian.PutUint16(hdr[12:], 32) // color depth
	hdr[32] = 1                                 // pixel width
	hdr[33] = 1                                 // pixel height
	return hdr
}

// frame0Pixels is a 16x16 image with a 4px semi-transparent border,
// four distinct opaque corners and a fully transparent center.
func frame0Pixels() []byte {
	pix := make([]byte, 16*16*4)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			var c color.NRGBA
			switch {
			case x < 4 && y < 4:
				c = color.NRGBA{255, 0, 0, 255} // top-left corner
			case x >= 12 && y < 4:
				c = color.NRGBA{0, 255, 0, 255} // top-right corner
			case x < 4 && y >= 12:
				c = color.NRGBA{0, 0, 255, 255} // bottom-left corner
			case x >= 12 && y >= 12:
				c = color.NRGBA{255, 255, 0, 255} // bottom-right corner
			case y < 4:
				c = color.NRGBA{100, 150, 200, 128} // top edge
			case y >= 12:
				c = color.NRGBA{90, 80, 70, 128} // bottom edge
			case x < 4:
				c = color.NRGBA{60, 50, 40, 128} // left edge
			case x >= 12:
				c = color.NRGBA{30, 20, 10, 128} // right edge
			default:
				// center stays transparent
				continue
			}
			i := (y*16 + x) * 4
			pix[i], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
	return pix
}

// frame1Pixels is a 16x16 image with a 2px semi-transparent border
// and an opaque center, in colors distinct from frame 0.
func frame1Pixels() []byte {
	pix := make([]byte, 16*16*4)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			var c color.NRGBA
			switch {
			case x < 2 && y < 2:
				c = color.NRGBA{11, 22, 33, 255} // top-left corner
			case x >= 14 && y < 2:
				c = color.NRGBA{44, 55, 66, 255} // top-right corner
			case x < 2 && y >= 14:
				c = color.NRGBA{77, 88, 99, 255} // bottom-left corner
			case x >= 14 && y >= 14:
				c = color.NRGBA{123, 234, 45, 255} // bottom-right corner
			case x < 2 || x >= 14 || y < 2 || y >= 14:
				c = color.NRGBA{10, 200, 90, 64} // edges
			default:
				c = color.NRGBA{1, 2, 3, 255} // center
			}
			i := (y*16 + x) * 4
			pix[i], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
	return pix
}

// nineSliceTestFile builds a synthetic two-frame 16x16 RGBA aseprite file.
// The "button" slice has a 9-slice key per frame with different centers,
// plus slices exercising the error paths and a duplicate name.
func nineSliceTestFile() []byte {
	frame0 := makeFrame(100,
		layerChunk(),
		celChunk(16, 16, frame0Pixels()),
		sliceChunk("button", 3, []sliceKey{
			{frame: 0, x: 0, y: 0, w: 16, h: 16, cx: 4, cy: 4, cw: 8, ch: 8, px: 8, py: 9},
			{frame: 1, x: 0, y: 0, w: 16, h: 16, cx: 2, cy: 2, cw: 12, ch: 12, px: 1, py: 1},
		}),
		// duplicate name: the first "button" slice must win
		sliceChunk("button", 3, []sliceKey{
			{frame: 0, x: 8, y: 8, w: 8, h: 8, cx: 1, cy: 1, cw: 6, ch: 6, px: 0, py: 0},
		}),
		sliceChunk("nocenter", 0, []sliceKey{
			{frame: 0, x: 0, y: 0, w: 16, h: 16},
		}),
		sliceChunk("badcenter", 1, []sliceKey{
			{frame: 0, x: 0, y: 0, w: 16, h: 16, cx: 10, cy: 10, cw: 20, ch: 20},
		}),
	)
	frame1 := makeFrame(100,
		celChunk(16, 16, frame1Pixels()),
	)
	body := append(frame0, frame1...)
	hdr := makeHeader(16, 16, 2, uint32(128+len(body)))
	return append(hdr, body...)
}

func readTestSprite(t *testing.T) *Aseprite {
	t.Helper()
	spr, err := Read(bytes.NewReader(nineSliceTestFile()))
	require.NoError(t, err)
	return spr
}

func requireColor(t *testing.T, img *image.NRGBA, x, y int, want color.NRGBA) {
	t.Helper()
	if got := img.NRGBAAt(x, y); got != want {
		t.Fatalf("pixel (%d,%d): got %v, want %v", x, y, got, want)
	}
}

func requireColorNear(t *testing.T, img *image.NRGBA, x, y int, want color.NRGBA, tol int) {
	t.Helper()
	got := img.NRGBAAt(x, y)
	near := func(a, b uint8) bool {
		d := int(a) - int(b)
		return d >= -tol && d <= tol
	}
	if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) || !near(got.A, want.A) {
		t.Fatalf("pixel (%d,%d): got %v, want %v ± %d", x, y, got, want, tol)
	}
}

func TestSlice(t *testing.T) {
	spr := readTestSprite(t)

	s, err := spr.Slice("button", 0)
	require.NoError(t, err)
	require.True(t, s.Frame == 0, "frame", s.Frame)
	require.True(t, s.Bounds == image.Rect(0, 0, 16, 16), "bounds", s.Bounds)
	require.True(t, s.Center == image.Rect(4, 4, 12, 12), "center", s.Center)
	require.True(t, s.Pivot == image.Pt(8, 9), "pivot", s.Pivot)

	// the second key applies to frame 1
	s, err = spr.Slice("button", 1)
	require.NoError(t, err)
	require.True(t, s.Frame == 1, "frame", s.Frame)
	require.True(t, s.Center == image.Rect(2, 2, 14, 14), "center", s.Center)
	require.True(t, s.Pivot == image.Pt(1, 1), "pivot", s.Pivot)

	_, err = spr.Slice("missing", 0)
	require.True(t, errors.Is(err, ErrSliceNotFound), "err", err)

	_, err = spr.Slice("button", -1)
	require.True(t, errors.Is(err, ErrFrameOutOfRange), "err", err)

	_, err = spr.Slice("button", 2)
	require.True(t, errors.Is(err, ErrFrameOutOfRange), "err", err)
}

func TestRenderSliceFrame0(t *testing.T) {
	spr := readTestSprite(t)

	img, err := spr.RenderSlice("button", 0, 32, 24)
	require.NoError(t, err)
	require.True(t, img.Bounds() == image.Rect(0, 0, 32, 24), "bounds", img.Bounds())

	// corners keep their original size and pixels; the red corner also
	// proves the first "button" slice wins over the duplicate name
	requireColor(t, img, 0, 0, color.NRGBA{255, 0, 0, 255})
	requireColor(t, img, 3, 3, color.NRGBA{255, 0, 0, 255})
	requireColor(t, img, 31, 0, color.NRGBA{0, 255, 0, 255})
	requireColor(t, img, 0, 23, color.NRGBA{0, 0, 255, 255})
	requireColor(t, img, 31, 23, color.NRGBA{255, 255, 0, 255})

	// edges are stretched along their own axis; semi-transparent pixels
	// allow a small tolerance for the premultiplied atlas quantization
	requireColorNear(t, img, 16, 1, color.NRGBA{100, 150, 200, 128}, 2)
	requireColorNear(t, img, 16, 22, color.NRGBA{90, 80, 70, 128}, 2)
	requireColorNear(t, img, 1, 12, color.NRGBA{60, 50, 40, 128}, 2)
	requireColorNear(t, img, 30, 12, color.NRGBA{30, 20, 10, 128}, 2)

	// the center is scaled in both directions; the source is transparent
	requireColor(t, img, 16, 12, color.NRGBA{0, 0, 0, 0})

	// where the translucent border fades into the transparent center the
	// hue is preserved (premultiplied-alpha interpolation, no dark edge);
	// naive NRGBA interpolation would give (25,37,50,32) and (15,12,10,32)
	requireColorNear(t, img, 16, 4, color.NRGBA{100, 150, 200, 32}, 2)
	requireColorNear(t, img, 4, 12, color.NRGBA{60, 50, 40, 43}, 2)
}

func TestRenderSliceFrame1(t *testing.T) {
	spr := readTestSprite(t)

	// frame 1 has a different 9-slice center (2px borders) and pixels
	img, err := spr.RenderSlice("button", 1, 32, 24)
	require.NoError(t, err)

	requireColor(t, img, 0, 0, color.NRGBA{11, 22, 33, 255})
	requireColor(t, img, 1, 1, color.NRGBA{11, 22, 33, 255})
	requireColor(t, img, 31, 0, color.NRGBA{44, 55, 66, 255})
	requireColor(t, img, 0, 23, color.NRGBA{77, 88, 99, 255})
	requireColor(t, img, 31, 23, color.NRGBA{123, 234, 45, 255})

	requireColorNear(t, img, 16, 1, color.NRGBA{10, 200, 90, 64}, 2)
	requireColorNear(t, img, 1, 12, color.NRGBA{10, 200, 90, 64}, 2)

	// with 2px borders, x=3 is already the scaled center
	requireColor(t, img, 3, 12, color.NRGBA{1, 2, 3, 255})
	requireColor(t, img, 16, 12, color.NRGBA{1, 2, 3, 255})
}

func TestRenderSliceExactBorders(t *testing.T) {
	spr := readTestSprite(t)

	// target size equal to the fixed borders leaves no center area
	img, err := spr.RenderSlice("button", 0, 8, 8)
	require.NoError(t, err)
	requireColor(t, img, 0, 0, color.NRGBA{255, 0, 0, 255})
	requireColor(t, img, 7, 0, color.NRGBA{0, 255, 0, 255})
	requireColor(t, img, 0, 7, color.NRGBA{0, 0, 255, 255})
	requireColor(t, img, 7, 7, color.NRGBA{255, 255, 0, 255})
}

func TestRenderSliceErrors(t *testing.T) {
	spr := readTestSprite(t)

	_, err := spr.RenderSlice("nocenter", 0, 32, 24)
	require.True(t, errors.Is(err, ErrNoNineSlice), "err", err)

	_, err = spr.RenderSlice("badcenter", 0, 32, 24)
	require.True(t, errors.Is(err, ErrInvalidCenter), "err", err)

	// fixed borders of frame 0 are 8x8
	_, err = spr.RenderSlice("button", 0, 7, 24)
	require.True(t, errors.Is(err, ErrTargetTooSmall), "err", err)

	_, err = spr.RenderSlice("button", 0, 32, 7)
	require.True(t, errors.Is(err, ErrTargetTooSmall), "err", err)

	_, err = spr.RenderSlice("missing", 0, 32, 24)
	require.True(t, errors.Is(err, ErrSliceNotFound), "err", err)

	_, err = spr.RenderSlice("button", -1, 32, 24)
	require.True(t, errors.Is(err, ErrFrameOutOfRange), "err", err)

	_, err = spr.RenderSlice("button", 2, 32, 24)
	require.True(t, errors.Is(err, ErrFrameOutOfRange), "err", err)
}
