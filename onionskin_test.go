package aseprite

import (
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/askeladdk/aseprite/internal/require"
)

// newOnionTestSprite builds a synthetic sprite of three 8x8 frames
// stacked vertically in the atlas. Each frame holds a 2x2 opaque block
// surrounded by a transparent border, at a different position:
//
//	frame 0: red   block at (1,1)
//	frame 1: green block at (3,3)
//	frame 2: blue  block at (5,5)
//
// The blocks never overlap, so ghost positions and alphas can be
// checked exactly.
func newOnionTestSprite() *Aseprite {
	atlas := image.NewNRGBA(image.Rect(0, 0, 8, 24))
	colors := []color.NRGBA{
		{200, 0, 0, 255},
		{0, 200, 0, 255},
		{0, 0, 200, 255},
	}
	frames := make([]Frame, 3)
	for i := range frames {
		frames[i] = Frame{Bounds: image.Rect(0, i*8, 8, (i+1)*8)}
		for y := 1 + 2*i; y < 3+2*i; y++ {
			for x := 1 + 2*i; x < 3+2*i; x++ {
				atlas.SetNRGBA(x, i*8+y, colors[i])
			}
		}
	}
	return &Aseprite{Image: atlas, Frames: frames}
}

func requirePixel(t *testing.T, img *image.NRGBA, x, y int, want color.NRGBA) {
	t.Helper()
	got := img.NRGBAAt(x, y)
	dr := int(got.R) - int(want.R)
	dg := int(got.G) - int(want.G)
	db := int(got.B) - int(want.B)
	da := int(got.A) - int(want.A)
	if dr < -1 || dr > 1 || dg < -1 || dg > 1 || db < -1 || db > 1 || da < -1 || da > 1 {
		t.Fatalf("pixel (%d,%d): got %v, want %v", x, y, got, want)
	}
}

func TestOnionSkinBasic(t *testing.T) {
	spr := newOnionTestSprite()

	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   1,
		Before:  1,
		After:   1,
		Opacity: 0.5,
		Falloff: 1,
	})
	require.NoError(t, err)

	// Same size as a single frame canvas.
	require.True(t, img.Bounds() == image.Rect(0, 0, 8, 8), "bounds", img.Bounds())

	// Ghosts sit at their own frame positions with faded alpha.
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 127}) // frame 0 ghost
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 127}) // frame 2 ghost
	requirePixel(t, img, 3, 3, color.NRGBA{0, 200, 0, 255}) // current frame on top
	requirePixel(t, img, 0, 0, color.NRGBA{0, 0, 0, 0})     // transparent border
	requirePixel(t, img, 7, 7, color.NRGBA{0, 0, 0, 0})     // transparent border
}

func TestOnionSkinFalloff(t *testing.T) {
	spr := newOnionTestSprite()

	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   2,
		Before:  2,
		Opacity: 0.5,
		Falloff: 0.5,
	})
	require.NoError(t, err)

	requirePixel(t, img, 3, 3, color.NRGBA{0, 200, 0, 127}) // d=1: 0.5   -> 127
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 63})  // d=2: 0.25  -> 63
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 255}) // current frame
}

func TestOnionSkinTint(t *testing.T) {
	spr := newOnionTestSprite()

	// Full-strength tint replaces the ghost color.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:      1,
		Before:     1,
		Opacity:    0.5,
		Falloff:    1,
		BeforeTint: color.NRGBA{0, 0, 255, 255},
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{0, 0, 255, 127})

	// Half-strength tint blends: (200,0,0) toward (0,0,255) by 128/255.
	img, err = spr.OnionSkin(OnionSkinOptions{
		Frame:      1,
		Before:     1,
		Opacity:    0.5,
		Falloff:    1,
		BeforeTint: color.NRGBA{0, 0, 255, 128},
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{99, 0, 128, 127})

	// AfterTint applies only to frames after the current one.
	img, err = spr.OnionSkin(OnionSkinOptions{
		Frame:     1,
		Before:    1,
		After:     1,
		Opacity:   0.5,
		Falloff:   1,
		AfterTint: color.NRGBA{255, 255, 255, 255},
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 127})     // untinted before ghost
	requirePixel(t, img, 5, 5, color.NRGBA{255, 255, 255, 127}) // tinted after ghost
}

func TestOnionSkinLinearClamp(t *testing.T) {
	spr := newOnionTestSprite()

	// Without a tag, neighbors are clamped at the range ends:
	// only frame 0 exists before frame 0.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   0,
		Before:  5,
		Opacity: 0.5,
		Falloff: 1,
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 255}) // current frame
	requirePixel(t, img, 3, 3, color.NRGBA{0, 0, 0, 0})     // no ghost
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 0, 0})     // no ghost
}

func TestOnionSkinTagForward(t *testing.T) {
	spr := newOnionTestSprite()
	spr.Tags = []Tag{{Name: "run", Lo: 0, Hi: 2, LoopDirection: Forward}}

	// At the Hi endpoint, the next frame loops back to Lo.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   2,
		After:   1,
		Opacity: 0.5,
		Falloff: 1,
		Tag:     "run",
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 127}) // wrapped to frame 0
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 255})

	// At the Lo endpoint, the previous frame loops back to Hi.
	img, err = spr.OnionSkin(OnionSkinOptions{
		Frame:   0,
		Before:  1,
		Opacity: 0.5,
		Falloff: 1,
		Tag:     "run",
	})
	require.NoError(t, err)
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 127}) // wrapped to frame 2
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 255})
}

func TestOnionSkinTagReverse(t *testing.T) {
	spr := newOnionTestSprite()
	spr.Tags = []Tag{{Name: "run", Lo: 0, Hi: 2, LoopDirection: Reverse}}

	// Reverse playback order is 2,1,0: after frame 2 comes frame 1,
	// and before frame 2 the cycle wraps to frame 0.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   2,
		Before:  1,
		After:   1,
		Opacity: 0.5,
		Falloff: 1,
		Tag:     "run",
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 127}) // before: frame 0
	requirePixel(t, img, 3, 3, color.NRGBA{0, 200, 0, 127}) // after: frame 1
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 255})
}

func TestTagPlaybackOrder(t *testing.T) {
	for _, tt := range []struct {
		Name string
		Tag  Tag
		Want []int
	}{
		{"forward", Tag{Lo: 0, Hi: 2, LoopDirection: Forward}, []int{0, 1, 2}},
		{"reverse", Tag{Lo: 0, Hi: 2, LoopDirection: Reverse}, []int{2, 1, 0}},
		{"pingpong", Tag{Lo: 0, Hi: 2, LoopDirection: PingPong}, []int{0, 1, 2, 1}},
		{"pingpong_reverse", Tag{Lo: 0, Hi: 2, LoopDirection: PingPongReverse}, []int{2, 1, 0, 1}},
		{"pingpong_two_frames", Tag{Lo: 0, Hi: 1, LoopDirection: PingPong}, []int{0, 1}},
		{"single_frame", Tag{Lo: 1, Hi: 1, LoopDirection: PingPong}, []int{1}},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			got := tagPlaybackOrder(tt.Tag)
			require.True(t, len(got) == len(tt.Want), "length", got)
			for i := range got {
				require.True(t, got[i] == tt.Want[i], "order", got, tt.Want)
			}
		})
	}
}

func TestOnionSkinTagPingPong(t *testing.T) {
	spr := newOnionTestSprite()
	spr.Tags = []Tag{{Name: "run", Lo: 0, Hi: 2, LoopDirection: PingPong}}

	// Playback order is 0,1,2,1. From frame 0: after = 1, 2, 1 (endpoints
	// are not duplicated); before wraps around the cycle to 1, 2, 1.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   0,
		Before:  3,
		After:   3,
		Opacity: 0.5,
		Falloff: 0.5,
		Tag:     "run",
	})
	require.NoError(t, err)

	// Frame 2 ghost appears at distance 2 in both directions:
	// alpha 0.5*0.5 -> 63, stacked twice -> 63 + 63*(255-63)/255 ≈ 110.
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 110})
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 255}) // current frame
}

func TestOnionSkinDuplicateTag(t *testing.T) {
	spr := newOnionTestSprite()
	spr.Tags = []Tag{
		{Name: "run", Lo: 0, Hi: 1, LoopDirection: Forward},
		{Name: "run", Lo: 1, Hi: 2, LoopDirection: Forward},
	}

	// The first tag with a matching name is used: after frame 0
	// comes frame 1, and frame 2 is never reached.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   0,
		After:   1,
		Opacity: 0.5,
		Falloff: 1,
		Tag:     "run",
	})
	require.NoError(t, err)
	requirePixel(t, img, 3, 3, color.NRGBA{0, 200, 0, 127})
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 0, 0})
}

func TestOnionSkinEmptyFrame(t *testing.T) {
	// Three frames where the middle frame is fully transparent.
	atlas := image.NewNRGBA(image.Rect(0, 0, 8, 24))
	for y := 1; y < 3; y++ {
		for x := 1; x < 3; x++ {
			atlas.SetNRGBA(x, y, color.NRGBA{200, 0, 0, 255}) // frame 0
		}
	}
	for y := 5; y < 7; y++ {
		for x := 5; x < 7; x++ {
			atlas.SetNRGBA(x, 16+y, color.NRGBA{0, 0, 200, 255}) // frame 2
		}
	}
	spr := &Aseprite{
		Image: atlas,
		Frames: []Frame{
			{Bounds: image.Rect(0, 0, 8, 8)},
			{Bounds: image.Rect(0, 8, 8, 16)}, // empty
			{Bounds: image.Rect(0, 16, 8, 24)},
		},
	}

	// The empty frame contributes nothing but causes no error.
	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   0,
		After:   2,
		Opacity: 0.5,
		Falloff: 1,
	})
	require.NoError(t, err)
	requirePixel(t, img, 1, 1, color.NRGBA{200, 0, 0, 255}) // current frame
	requirePixel(t, img, 3, 3, color.NRGBA{0, 0, 0, 0})     // empty frame 1 ghost
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 200, 127}) // frame 2 ghost
}

func TestOnionSkinZeroNeighbors(t *testing.T) {
	spr := newOnionTestSprite()

	img, err := spr.OnionSkin(OnionSkinOptions{
		Frame:   1,
		Before:  0,
		After:   0,
		Opacity: 0.5,
		Falloff: 1,
	})
	require.NoError(t, err)
	requirePixel(t, img, 3, 3, color.NRGBA{0, 200, 0, 255}) // current frame only
	requirePixel(t, img, 1, 1, color.NRGBA{0, 0, 0, 0})
	requirePixel(t, img, 5, 5, color.NRGBA{0, 0, 0, 0})
}

func TestOnionSkinErrors(t *testing.T) {
	spr := newOnionTestSprite()
	spr.Tags = []Tag{{Name: "run", Lo: 0, Hi: 1, LoopDirection: Forward}}

	for _, tt := range []struct {
		Name string
		Opts OnionSkinOptions
		Err  error
	}{
		{"negative_frame", OnionSkinOptions{Frame: -1, Opacity: 0.5, Falloff: 1}, ErrFrameOutOfRange},
		{"frame_too_large", OnionSkinOptions{Frame: 3, Opacity: 0.5, Falloff: 1}, ErrFrameOutOfRange},
		{"missing_tag", OnionSkinOptions{Frame: 0, Opacity: 0.5, Falloff: 1, Tag: "jump"}, ErrTagNotFound},
		{"frame_not_in_tag", OnionSkinOptions{Frame: 2, Opacity: 0.5, Falloff: 1, Tag: "run"}, ErrFrameNotInTag},
		{"negative_opacity", OnionSkinOptions{Frame: 0, Opacity: -0.1, Falloff: 1}, ErrInvalidOpacity},
		{"opacity_too_large", OnionSkinOptions{Frame: 0, Opacity: 1.1, Falloff: 1}, ErrInvalidOpacity},
		{"negative_falloff", OnionSkinOptions{Frame: 0, Opacity: 0.5, Falloff: -1}, ErrInvalidOpacity},
		{"falloff_too_large", OnionSkinOptions{Frame: 0, Opacity: 0.5, Falloff: 2}, ErrInvalidOpacity},
	} {
		t.Run(tt.Name, func(t *testing.T) {
			_, err := spr.OnionSkin(tt.Opts)
			require.True(t, errors.Is(err, tt.Err), "error", err)
		})
	}
}
