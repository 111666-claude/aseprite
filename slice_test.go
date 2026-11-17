package aseprite

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/askeladdk/aseprite/internal/require"
)

// Minimal aseprite binary builder used to construct deterministic fixtures
// without depending on the Aseprite application.

func putU16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
func putU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

func u16(v uint16) []byte {
	b := make([]byte, 2)
	putU16(b, v)
	return b
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	putU32(b, v)
	return b
}

func s32(v int32) []byte { return u32(uint32(v)) }

func str(s string) []byte { return append(u16(uint16(len(s))), s...) }

func chunkBytes(typ uint16, payload []byte) []byte {
	b := u32(uint32(6 + len(payload)))
	b = append(b, u16(typ)...)
	return append(b, payload...)
}

type testSliceKey struct {
	frame  uint32
	x, y   int32
	w, h   uint32
	cx, cy int32
	cw, ch uint32
	px, py int32
}

func sliceChunk(name string, flags uint32, keys []testSliceKey) []byte {
	var p []byte
	p = append(p, u32(uint32(len(keys)))...)
	p = append(p, u32(flags)...)
	p = append(p, u32(0)...) // reserved
	p = append(p, str(name)...)

	for _, k := range keys {
		p = append(p, u32(k.frame)...)
		p = append(p, s32(k.x)...)
		p = append(p, s32(k.y)...)
		p = append(p, u32(k.w)...)
		p = append(p, u32(k.h)...)
		if flags&1 != 0 {
			p = append(p, s32(k.cx)...)
			p = append(p, s32(k.cy)...)
			p = append(p, u32(k.cw)...)
			p = append(p, u32(k.ch)...)
		}
		if flags&2 != 0 {
			p = append(p, s32(k.px)...)
			p = append(p, s32(k.py)...)
		}
	}

	return chunkBytes(0x2022, p)
}

func userDataChunk(text string, c color.NRGBA) []byte {
	var flags uint32 = 2 // has color
	var p []byte
	if text != "" {
		flags |= 1
	}
	p = append(p, u32(flags)...)
	if text != "" {
		p = append(p, str(text)...)
	}
	p = append(p, c.R, c.G, c.B, c.A)
	return chunkBytes(0x2020, p)
}

func frameBytes(duration uint16, chunks ...[]byte) []byte {
	var body []byte
	for _, c := range chunks {
		body = append(body, c...)
	}

	hdr := make([]byte, 16)
	putU32(hdr[0:], uint32(16+len(body)))
	putU16(hdr[4:], 0xF1FA)
	putU16(hdr[6:], uint16(len(chunks))) // old chunk count
	putU16(hdr[8:], duration)
	putU32(hdr[12:], uint32(len(chunks))) // new chunk count
	return append(hdr, body...)
}

// buildAseprite assembles a 32bpp RGBA sprite with no layers or cels.
func buildAseprite(nframes, w, h uint16, frames ...[]byte) []byte {
	hdr := make([]byte, 128)
	putU16(hdr[4:], 0xA5E0) // magic
	putU16(hdr[6:], nframes)
	putU16(hdr[8:], w)
	putU16(hdr[10:], h)
	putU16(hdr[12:], 32) // bpp
	putU16(hdr[14:], 1)  // flags
	putU16(hdr[32:], 1)  // palette entries (1 even for RGB, as Aseprite writes)
	hdr[34] = 1          // pixel ratio
	hdr[35] = 1

	var b []byte
	b = append(b, hdr...)
	for _, fr := range frames {
		b = append(b, fr...)
	}
	putU32(b[0:], uint32(len(b)))
	return b
}

// fixtureSlices builds the fixed three-frame, two-slice file.
//
// Frame 0 holds the "hitbox" slice chunk (9-patches + pivot, two keys stored
// out of order) followed by a user data chunk. Frame 1 is empty. Frame 2 holds
// the "grab" slice chunk (single key, no center, no pivot), proving that slice
// chunks are collected from every frame.
func fixtureSlices() []byte {
	hitbox := sliceChunk("hitbox", 3, []testSliceKey{
		// Stored out of order: frame 2 first, frame 0 second.
		{frame: 2, x: 2, y: 2, w: 5, h: 5, cx: 1, cy: 1, cw: 3, ch: 3, px: 2, py: 3},
		{frame: 0, x: 0, y: 0, w: 6, h: 6, cx: 1, cy: 1, cw: 4, ch: 4, px: 3, py: 3},
	})
	hitdata := userDataChunk("hitbox", color.NRGBA{255, 0, 0, 255})

	grab := sliceChunk("grab", 0, []testSliceKey{
		{frame: 2, x: 4, y: 4, w: 4, h: 4},
	})

	return buildAseprite(3, 8, 8,
		frameBytes(100, hitbox, hitdata),
		frameBytes(100),
		frameBytes(100, grab),
	)
}

// TestGenSliceFixture regenerates testfiles/slices.aseprite. Run with:
//
//	GEN_SLICE_FIXTURE=1 go test -run TestGenSliceFixture
func TestGenSliceFixture(t *testing.T) {
	if os.Getenv("GEN_SLICE_FIXTURE") == "" {
		t.Skip("set GEN_SLICE_FIXTURE to regenerate the fixture")
	}
	require.NoError(t, os.WriteFile("./testfiles/slices.aseprite", fixtureSlices(), 0o644))
}

func TestSliceKeyframes(t *testing.T) {
	// Decode through the standard image.Decode entry point, like the
	// existing decode tests, and verify the original decoding still works.
	f, err := os.Open("./testfiles/slices.aseprite")
	require.NoError(t, err)
	defer f.Close()

	img, imgformat, err := image.Decode(f)
	require.NoError(t, err)
	require.True(t, imgformat == "aseprite", "image format", imgformat)

	spr, ok := img.(*Aseprite)
	require.True(t, ok)
	require.True(t, len(spr.Frames) == 3, "frames", len(spr.Frames))
	require.True(t, len(spr.Slices) == 2, "slices", len(spr.Slices))

	// Atlas layout for three 8x8 frames: 2x2 grid.
	wantFrameBounds := []image.Rectangle{
		image.Rect(0, 0, 8, 8),
		image.Rect(8, 0, 16, 8),
		image.Rect(0, 8, 8, 16),
	}
	for i, want := range wantFrameBounds {
		require.True(t, spr.Frames[i].Bounds.Eq(want), "frame bounds", i, spr.Frames[i].Bounds)
	}

	// ---- hitbox: two keys, out of order in the file, sorted after parsing.
	hitbox := spr.Slices[0]
	require.True(t, hitbox.Name == "hitbox")
	require.True(t, len(hitbox.Keys) == 2, "keys", len(hitbox.Keys))
	require.True(t, hitbox.Keys[0].Frame == 0 && hitbox.Keys[1].Frame == 2, "key order")

	// Raw keys keep sprite (canvas) coordinates and the original frame number.
	require.True(t, hitbox.Keys[0].Bounds.Eq(image.Rect(0, 0, 6, 6)), "key0 bounds")
	require.True(t, hitbox.Keys[1].Bounds.Eq(image.Rect(2, 2, 7, 7)), "key1 bounds")
	require.True(t, hitbox.Keys[0].Center.Eq(image.Rect(1, 1, 5, 5)), "key0 center")
	require.True(t, hitbox.Keys[1].Center.Eq(image.Rect(1, 1, 4, 4)), "key1 center")
	require.True(t, hitbox.Keys[0].Pivot.Eq(image.Pt(3, 3)), "key0 pivot")
	require.True(t, hitbox.Keys[1].Pivot.Eq(image.Pt(2, 3)), "key1 pivot")

	// Static fields stay compatible: first key, mapped to its atlas frame.
	require.True(t, hitbox.Bounds.Eq(image.Rect(0, 0, 6, 6)), "static bounds")
	require.True(t, hitbox.Center.Eq(image.Rect(1, 1, 5, 5)), "static center")
	require.True(t, hitbox.Pivot.Eq(image.Pt(3, 3)), "static pivot")
	require.True(t, string(hitbox.Data) == "hitbox", "user data")
	require.True(t, hitbox.Color == color.Color(color.NRGBA{255, 0, 0, 255}), "color")

	// Name + frame queries, mapped to the frame's atlas position.
	s0, ok := spr.Slice("hitbox", 0)
	require.True(t, ok)
	require.True(t, s0.Bounds.Eq(image.Rect(0, 0, 6, 6)), "hitbox f0")
	require.True(t, s0.Center.Eq(image.Rect(1, 1, 5, 5)))
	require.True(t, s0.Pivot.Eq(image.Pt(3, 3)))

	// Frame 1 inherits the frame-0 definition but lands at a different atlas tile.
	s1, ok := spr.Slice("hitbox", 1)
	require.True(t, ok)
	require.True(t, s1.Bounds.Eq(image.Rect(8, 0, 14, 6)), "hitbox f1 inherited")
	require.True(t, s1.Center.Eq(image.Rect(1, 1, 5, 5)), "center stays relative")
	require.True(t, s1.Pivot.Eq(image.Pt(3, 3)), "pivot stays relative")

	// Frame 2 uses its own keyframe.
	s2, ok := spr.Slice("hitbox", 2)
	require.True(t, ok)
	require.True(t, s2.Bounds.Eq(image.Rect(2, 10, 7, 15)), "hitbox f2")
	require.True(t, s2.Center.Eq(image.Rect(1, 1, 4, 4)))
	require.True(t, s2.Pivot.Eq(image.Pt(2, 3)))

	// Slice.Key exposes raw sprite-coordinate keys directly.
	k1, ok := hitbox.Key(1)
	require.True(t, ok)
	require.True(t, k1.Frame == 0 && k1.Bounds.Eq(image.Rect(0, 0, 6, 6)), "raw inherited key")

	// ---- grab: single key starting at frame 2, no center and no pivot.
	grab := spr.Slices[1]
	require.True(t, grab.Name == "grab")
	require.True(t, len(grab.Keys) == 1 && grab.Keys[0].Frame == 2)
	require.True(t, grab.Center == image.Rectangle{}, "absent center is zero")
	require.True(t, grab.Pivot == image.Point{}, "absent pivot is zero")
	require.True(t, grab.Bounds.Eq(image.Rect(4, 12, 8, 16)), "static grab bounds mapped")

	if _, ok := spr.Slice("grab", 0); ok {
		t.Fatal("grab must not be active before its first keyframe")
	}
	if _, ok := spr.Slice("grab", 1); ok {
		t.Fatal("grab must not be active before its first keyframe")
	}
	g2, ok := spr.Slice("grab", 2)
	require.True(t, ok)
	require.True(t, g2.Bounds.Eq(image.Rect(4, 12, 8, 16)), "grab f2")
	require.True(t, g2.Center == image.Rectangle{} && g2.Pivot == image.Point{})

	// ---- deterministic misses.
	if _, ok := spr.Slice("missing", 0); ok {
		t.Fatal("unknown slice name must miss")
	}
	if _, ok := spr.Slice("hitbox", -1); ok {
		t.Fatal("negative frame must miss")
	}
	if _, ok := spr.Slice("hitbox", 3); ok {
		t.Fatal("frame beyond the last frame must miss")
	}
	if _, ok := hitbox.Key(-1); ok {
		t.Fatal("Key before first keyframe must miss")
	}
}

func decodeBytes(t *testing.T, data []byte) *Aseprite {
	t.Helper()
	spr, err := Read(bytes.NewReader(data))
	require.NoError(t, err)
	return spr
}

func TestSliceEdgeCases(t *testing.T) {
	t.Run("duplicate names resolve to the first", func(t *testing.T) {
		data := buildAseprite(1, 8, 8,
			frameBytes(100,
				sliceChunk("dup", 0, []testSliceKey{{frame: 0, x: 1, y: 1, w: 2, h: 2}}),
				sliceChunk("dup", 0, []testSliceKey{{frame: 0, x: 3, y: 3, w: 2, h: 2}}),
			),
		)
		spr := decodeBytes(t, data)
		require.True(t, len(spr.Slices) == 2, "both slices retained")
		s, ok := spr.Slice("dup", 0)
		require.True(t, ok)
		require.True(t, s.Bounds.Eq(image.Rect(1, 1, 3, 3)), "first duplicate wins")
	})

	t.Run("out-of-range key frame", func(t *testing.T) {
		data := buildAseprite(3, 8, 8,
			frameBytes(100,
				sliceChunk("late", 0, []testSliceKey{
					{frame: 0, x: 0, y: 0, w: 1, h: 1},
					{frame: 99, x: 2, y: 2, w: 1, h: 1},
				}),
			),
			frameBytes(100),
			frameBytes(100),
		)
		spr := decodeBytes(t, data)
		s := spr.Slices[0]
		require.True(t, len(s.Keys) == 2, "out-of-range key retained")
		// Static fields map the first key; the out-of-range key is never active.
		require.True(t, s.Bounds.Eq(image.Rect(0, 0, 1, 1)))
		if k, ok := s.Key(2); !ok || k.Frame != 0 {
			t.Fatal("in-range frames must use the frame-0 key")
		}
		if _, ok := spr.Slice("late", 2); !ok {
			t.Fatal("slice stays active through in-range frames")
		}
	})

	t.Run("first key out of range keeps canvas bounds", func(t *testing.T) {
		data := buildAseprite(1, 8, 8,
			frameBytes(100,
				sliceChunk("future", 0, []testSliceKey{
					{frame: 7, x: 5, y: 5, w: 1, h: 1},
				}),
			),
		)
		spr := decodeBytes(t, data)
		s := spr.Slices[0]
		require.True(t, len(s.Keys) == 1)
		// No atlas frame exists for frame 7: static bounds stay in canvas space.
		require.True(t, s.Bounds.Eq(image.Rect(5, 5, 6, 6)), "fallback bounds")
		if _, ok := spr.Slice("future", 0); ok {
			t.Fatal("future key must not be active at frame 0")
		}
	})

	t.Run("truncated key payload keeps complete keys", func(t *testing.T) {
		// Two keys of 44 bytes each; cut the chunk after the first key while
		// fixing the chunk size so the file framing stays intact.
		full := sliceChunk("cut", 3, []testSliceKey{
			{frame: 0, x: 0, y: 0, w: 4, h: 4, cx: 0, cy: 0, cw: 4, ch: 4, px: 1, py: 1},
			{frame: 1, x: 1, y: 1, w: 4, h: 4, cx: 0, cy: 0, cw: 4, ch: 4, px: 1, py: 1},
		})
		header := full[:6]
		payload := full[6:]
		// header(20) + name "cut" (5) = 25 bytes; one full key is 44.
		cutPayload := payload[:25+44]
		cutChunk := u32(uint32(6 + len(cutPayload)))
		cutChunk = append(cutChunk, header[4:]...)
		cutChunk = append(cutChunk, cutPayload...)

		data := buildAseprite(2, 8, 8, frameBytes(100, cutChunk))
		spr := decodeBytes(t, data)
		require.True(t, len(spr.Slices) == 1)
		require.True(t, len(spr.Slices[0].Keys) == 1, "truncated key dropped, complete key kept")
		require.True(t, spr.Slices[0].Keys[0].Frame == 0)
	})

	t.Run("truncated chunk header skips the chunk", func(t *testing.T) {
		payload := []byte{1, 0, 0, 0, 3, 0, 0, 0, 0, 0}
		data := buildAseprite(1, 8, 8, frameBytes(100, chunkBytes(0x2022, payload)))
		spr := decodeBytes(t, data)
		require.True(t, len(spr.Slices) == 0, "malformed header yields no slices")
	})

	t.Run("truncated file returns an error", func(t *testing.T) {
		data := fixtureSlices()
		if _, err := Read(bytes.NewReader(data[:len(data)-40])); err == nil {
			t.Fatal("truncated chunk must error deterministically")
		}
		// Cut inside a frame header, before any chunk can be read.
		if _, err := Read(bytes.NewReader(data[:128+8])); err == nil {
			t.Fatal("truncated frame header must error deterministically")
		}
	})

	t.Run("zero-size hidden key", func(t *testing.T) {
		data := buildAseprite(2, 8, 8,
			frameBytes(100,
				sliceChunk("hide", 0, []testSliceKey{
					{frame: 0, x: 0, y: 0, w: 4, h: 4},
					{frame: 1, x: 0, y: 0, w: 0, h: 0},
				}),
			),
			frameBytes(100),
		)
		spr := decodeBytes(t, data)
		s0, ok := spr.Slice("hide", 0)
		require.True(t, ok)
		require.True(t, !s0.Bounds.Empty(), "frame 0 is visible")
		s, ok := spr.Slice("hide", 1)
		require.True(t, ok, "hidden slice still resolves")
		require.True(t, s.Bounds.Empty(), "zero-width key has empty bounds")
	})
}
