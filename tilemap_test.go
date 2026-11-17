package aseprite

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/askeladdk/aseprite/internal/require"
)

// Tile flip flags as stored in the high bits of a 32-bit tilemap tile.
const (
	tileXFlip = 0x20000000
	tileYFlip = 0x40000000
	tileDFlip = 0x80000000
)

// chunkWriter builds chunk payloads.
type chunkWriter struct {
	bytes.Buffer
}

func (w *chunkWriter) u8(v byte)    { w.WriteByte(v) }
func (w *chunkWriter) u16(v uint16) { binary.Write(w, binary.LittleEndian, v) }
func (w *chunkWriter) u32(v uint32) { binary.Write(w, binary.LittleEndian, v) }

func (w *chunkWriter) str(s string) {
	w.u16(uint16(len(s)))
	w.WriteString(s)
}

func makeChunk(typ uint16, fill func(w *chunkWriter)) []byte {
	var payload chunkWriter
	fill(&payload)

	var w chunkWriter
	w.u32(uint32(payload.Len() + 6))
	w.u16(typ)

	return append(w.Bytes(), payload.Bytes()...)
}

func makeFrame(chunks ...[]byte) []byte {
	var body []byte
	for _, c := range chunks {
		body = append(body, c...)
	}

	var w chunkWriter
	w.u32(uint32(16 + len(body)))
	w.u16(0xF1FA) // frame magic
	w.u16(uint16(len(chunks)))
	w.u16(100) // duration ms
	w.u16(0)   // reserved
	w.u32(uint32(len(chunks)))

	return append(w.Bytes(), body...)
}

func makeFile(width, height, bpp uint16, frames ...[]byte) []byte {
	var body []byte
	for _, fr := range frames {
		body = append(body, fr...)
	}

	hdr := make([]byte, 128)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(128+len(body)))
	binary.LittleEndian.PutUint16(hdr[4:], 0xA5E0) // magic
	binary.LittleEndian.PutUint16(hdr[6:], uint16(len(frames)))
	binary.LittleEndian.PutUint16(hdr[8:], width)
	binary.LittleEndian.PutUint16(hdr[10:], height)
	binary.LittleEndian.PutUint16(hdr[12:], bpp)
	binary.LittleEndian.PutUint32(hdr[14:], 1) // layer opacity valid
	binary.LittleEndian.PutUint16(hdr[18:], 100)
	binary.LittleEndian.PutUint16(hdr[32:], 256) // number of colors
	hdr[34], hdr[35] = 1, 1                      // pixel ratio

	return append(hdr, body...)
}

func layerChunk(name string, typ uint16, tileset uint32) []byte {
	return makeChunk(0x2004, func(w *chunkWriter) {
		w.u16(1)   // visible
		w.u16(typ) // 0 = normal, 2 = tilemap
		w.u16(0)   // child level
		w.u16(0)   // default width
		w.u16(0)   // default height
		w.u16(0)   // blend mode normal
		w.u8(255)  // opacity
		w.u8(0)
		w.u8(0)
		w.u8(0)
		w.str(name)
		if typ == 2 {
			w.u32(tileset)
		}
	})
}

// tilesetChunk builds a tileset chunk. zdata is the (already zlib
// compressed) tile image data, used when flags bit 1 is set.
func tilesetChunk(flags uint32, ntiles uint32, tilew, tileh uint16, zdata []byte) []byte {
	return makeChunk(0x2023, func(w *chunkWriter) {
		w.u32(0) // tileset id
		w.u32(flags)
		w.u32(ntiles)
		w.u16(tilew)
		w.u16(tileh)
		w.u16(1) // base index
		w.Write(make([]byte, 14))
		w.str("tiles")
		if flags&2 != 0 {
			w.u32(uint32(len(zdata)))
			w.Write(zdata)
		}
	})
}

func celChunk(layer uint16, x, y int16, celtype uint16, fill func(w *chunkWriter)) []byte {
	return makeChunk(0x2005, func(w *chunkWriter) {
		w.u16(layer)
		w.u16(uint16(x))
		w.u16(uint16(y))
		w.u8(255) // opacity
		w.u16(celtype)
		w.u16(0) // z-index
		w.Write(make([]byte, 5))
		fill(w)
	})
}

func imageCelChunk(layer uint16, x, y int16, width, height uint16, pix []byte) []byte {
	return celChunk(layer, x, y, 2, func(w *chunkWriter) {
		w.u16(width)
		w.u16(height)
		w.Write(zcompress(pix))
	})
}

func linkedCelChunk(layer uint16, x, y int16, frame uint16) []byte {
	return celChunk(layer, x, y, 1, func(w *chunkWriter) {
		w.u16(frame)
	})
}

func tilemapCelChunkZ(layer uint16, x, y int16, width, height uint16, zdata []byte) []byte {
	return celChunk(layer, x, y, 3, func(w *chunkWriter) {
		w.u16(width)
		w.u16(height)
		w.u16(32) // bits per tile
		w.u32(0x1FFFFFFF)
		w.u32(tileXFlip)
		w.u32(tileYFlip)
		w.u32(tileDFlip)
		w.Write(make([]byte, 10))
		w.Write(zdata)
	})
}

func tilemapCelChunk(layer uint16, x, y int16, width, height uint16, tiles []uint32) []byte {
	raw := make([]byte, len(tiles)*4)
	for i, t := range tiles {
		binary.LittleEndian.PutUint32(raw[i*4:], t)
	}
	return tilemapCelChunkZ(layer, x, y, width, height, zcompress(raw))
}

func zcompress(data []byte) []byte {
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write(data)
	zw.Close()
	return b.Bytes()
}

// tilesetPixels returns the raw 32bpp pixels of a 4-tile 8x8 tileset:
//
//	tile 0: empty
//	tile 1: opaque red, transparent top-left corner pixel
//	tile 2: opaque green, white marker pixel at (1,2)
//	tile 3: half-transparent blue, white marker pixel at (1,3)
func tilesetPixels() []byte {
	pix := make([]byte, 4*8*8*4)

	set := func(tile, x, y int, c color.NRGBA) {
		i := (tile*8+y)*8*4 + x*4
		pix[i+0] = c.R
		pix[i+1] = c.G
		pix[i+2] = c.B
		pix[i+3] = c.A
	}

	red := color.NRGBA{255, 0, 0, 255}
	green := color.NRGBA{0, 255, 0, 255}
	blue := color.NRGBA{0, 0, 255, 128}
	white := color.NRGBA{255, 255, 255, 255}

	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			set(1, x, y, red)
			set(2, x, y, green)
			set(3, x, y, blue)
		}
	}

	set(1, 0, 0, color.NRGBA{})
	set(2, 1, 2, white)
	set(3, 1, 3, white)

	return pix
}

// charPixels returns a 4x4 opaque yellow image cel.
func charPixels() []byte {
	pix := make([]byte, 4*4*4)
	for i := 0; i < 4*4; i++ {
		pix[i*4+0] = 255
		pix[i*4+1] = 255
		pix[i*4+2] = 0
		pix[i*4+3] = 255
	}
	return pix
}

// tilemapFixture builds a two-frame 32x32 32bpp sprite with a tilemap
// layer ("ground") below a normal image layer ("char").
//
// Frame 0: 2x2 tilemap cel at (2,3) exercising x, y and diagonal flips,
// plus a 4x4 image cel at (4,4) overlapping the tilemap.
// Frame 1: 2x2 tilemap cel at (0,0) with an empty tile and a combined
// x+y flip, plus a linked cel referencing frame 0's image cel.
func tilemapFixture() []byte {
	frame0 := makeFrame(
		layerChunk("ground", 2, 0),
		layerChunk("char", 0, 0),
		tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
		tilemapCelChunk(0, 2, 3, 2, 2, []uint32{
			1, 2 | tileXFlip,
			2 | tileYFlip, 3 | tileDFlip,
		}),
		imageCelChunk(1, 4, 4, 4, 4, charPixels()),
	)

	frame1 := makeFrame(
		tilemapCelChunk(0, 0, 0, 2, 2, []uint32{
			2, 0,
			1, 3 | tileXFlip | tileYFlip,
		}),
		linkedCelChunk(1, 4, 4, 0),
	)

	return makeFile(32, 32, 32, frame0, frame1)
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestDecodeTilemap(t *testing.T) {
	aspr, err := Read(bytes.NewReader(tilemapFixture()))
	require.NoError(t, err)

	require.True(t, len(aspr.Frames) == 2, "frames", len(aspr.Frames))
	require.True(t, len(aspr.LayerData) == 0)

	// atlas holds two 32x32 frames side by side
	require.True(t, aspr.Frames[0].Bounds == image.Rect(0, 0, 32, 32), "frame 0 bounds", aspr.Frames[0].Bounds)
	require.True(t, aspr.Frames[1].Bounds == image.Rect(32, 0, 64, 32), "frame 1 bounds", aspr.Frames[1].Bounds)

	transparent := color.NRGBA{}
	red := color.NRGBA{255, 0, 0, 255}
	green := color.NRGBA{0, 255, 0, 255}
	blue := color.NRGBA{0, 0, 255, 128}
	white := color.NRGBA{255, 255, 255, 255}
	yellow := color.NRGBA{255, 255, 0, 255}

	for _, tt := range []struct {
		Name string
		X, Y int
		Want color.NRGBA
	}{
		// frame 0
		{"f0 outside cels", 0, 0, transparent},
		{"f0 tile1 transparent corner", 2, 3, transparent},
		{"f0 tile1 red", 3, 3, red},
		{"f0 tile1 red at tile edge", 9, 10, red},
		{"f0 xflip marker", 16, 5, white}, // local (1,2) -> (6,2)
		{"f0 xflip unmarked pos", 11, 5, green},
		{"f0 yflip marker", 3, 16, white}, // local (1,2) -> (1,5)
		{"f0 yflip unmarked pos", 3, 13, green},
		{"f0 dflip marker", 13, 12, white}, // local (1,3) -> (3,1)
		{"f0 dflip unmarked pos", 11, 14, blue},
		{"f0 tile3 alpha", 12, 12, blue},
		{"f0 char over tilemap", 5, 5, yellow},
		{"f0 char edge", 7, 7, yellow},
		{"f0 tilemap below char edge", 8, 8, red},
		// frame 1 (atlas x offset 32)
		{"f1 tile2 marker", 33, 2, white},
		{"f1 empty tile", 40, 0, transparent},
		{"f1 tile1 transparent corner", 32, 8, transparent},
		{"f1 tile1 red", 33, 8, red},
		{"f1 xyflip marker", 46, 12, white}, // local (1,3) -> (6,4)
		{"f1 xyflip blue", 41, 9, blue},
		{"f1 linked char cel", 37, 5, yellow},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			got := nrgbaAt(aspr.Image, tt.X, tt.Y)
			require.True(t, got == tt.Want, "at", tt.X, tt.Y, "got", got, "want", tt.Want)
		})
	}
}

func TestDecodeTilemapConfig(t *testing.T) {
	conf, format, err := image.DecodeConfig(bytes.NewReader(tilemapFixture()))
	require.NoError(t, err)
	require.True(t, format == "aseprite", "format", format)
	require.True(t, conf.Width == 64 && conf.Height == 32, "size", conf.Width, conf.Height)
	require.True(t, conf.ColorModel == color.RGBAModel, "color model")
}

func requireErrorContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error containing", substr, "got nil")
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatal("expected error containing", substr, "got", err)
	}
}

func TestTilemapErrors(t *testing.T) {
	t.Run("external tileset", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(1|2, 4, 8, 8, zcompress(tilesetPixels())),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "external")
	})

	t.Run("invalid tile index", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunk(0, 0, 0, 2, 2, []uint32{9, 1, 2, 3}),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "tile index")
	})

	t.Run("truncated tileset data", func(t *testing.T) {
		zdata := zcompress(tilesetPixels())
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 8, 8, zdata[:len(zdata)/2]),
		))
		_, err := Read(bytes.NewReader(f))
		require.True(t, err != nil, "expected error")
	})

	t.Run("truncated tilemap data", func(t *testing.T) {
		raw := make([]byte, 4*4)
		binary.LittleEndian.PutUint32(raw[0:], 1)
		zdata := zcompress(raw)
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunkZ(0, 0, 0, 2, 2, zdata[:len(zdata)/2]),
		))
		_, err := Read(bytes.NewReader(f))
		require.True(t, err != nil, "expected error")
	})

	t.Run("tilemap data size mismatch", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunk(0, 0, 0, 2, 2, []uint32{1, 2, 3}), // 3 tiles, want 4
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "size")
	})

	t.Run("invalid tileset dimensions", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 0, 8, zcompress(tilesetPixels())),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "dimensions")
	})

	t.Run("invalid tilemap dimensions", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunk(0, 0, 0, 0, 2, []uint32{1, 2}),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "dimensions")
	})

	t.Run("invalid layer index", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 0),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunk(9, 0, 0, 2, 2, []uint32{1, 2, 3, 1}),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "layer index")
	})

	t.Run("invalid tileset index", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("ground", 2, 5),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunk(0, 0, 0, 2, 2, []uint32{1, 2, 3, 1}),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "tileset index")
	})

	t.Run("tilemap cel on normal layer", func(t *testing.T) {
		f := makeFile(16, 16, 32, makeFrame(
			layerChunk("char", 0, 0),
			tilesetChunk(2|4, 4, 8, 8, zcompress(tilesetPixels())),
			tilemapCelChunk(0, 0, 0, 2, 2, []uint32{1, 2, 3, 1}),
		))
		_, err := Read(bytes.NewReader(f))
		requireErrorContains(t, err, "non-tilemap")
	})
}
