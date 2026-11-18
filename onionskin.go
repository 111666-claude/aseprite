package aseprite

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
)

// Errors returned by OnionSkin.
var (
	// ErrFrameOutOfRange is returned when the current frame index
	// is not a valid frame of the sprite.
	ErrFrameOutOfRange = errors.New("aseprite: frame index out of range")

	// ErrTagNotFound is returned when the named tag does not exist.
	ErrTagNotFound = errors.New("aseprite: tag not found")

	// ErrFrameNotInTag is returned when the current frame
	// lies outside the frame range of the selected tag.
	ErrFrameNotInTag = errors.New("aseprite: frame is outside the tag's frame range")

	// ErrInvalidOpacity is returned when Opacity or Falloff
	// is outside the interval [0, 1].
	ErrInvalidOpacity = errors.New("aseprite: opacity and falloff must be between 0 and 1")
)

// OnionSkinOptions configures an onion skin preview.
type OnionSkinOptions struct {
	// Frame is the index of the current frame.
	// It is always drawn untinted, at full opacity, on top.
	Frame int

	// Before is the number of neighboring frames drawn before
	// the current frame in playback order. Negative values are
	// treated as zero. Zero Before and After yields a preview
	// containing only the current frame.
	Before int

	// After is the number of neighboring frames drawn after
	// the current frame in playback order. Negative values are
	// treated as zero.
	After int

	// Opacity is the opacity of the nearest neighboring frames
	// in the interval [0, 1]. The neighbor at distance d is drawn
	// with opacity Opacity * Falloff^(d-1).
	Opacity float64

	// Falloff is the per-step opacity multiplier in the interval
	// [0, 1]. A falloff of 1 keeps all neighbors equally opaque.
	Falloff float64

	// BeforeTint, if not nil, tints the frames before the current
	// frame. The tint color's alpha controls the blend strength:
	// 255 replaces the frame colors entirely, 0 leaves them unchanged.
	BeforeTint color.Color

	// AfterTint, if not nil, tints the frames after the current
	// frame. See BeforeTint.
	AfterTint color.Color

	// Tag, if not empty, selects neighbors by the playback order
	// of the first tag with this name, looping at the endpoints
	// according to the tag's LoopDirection. If empty, neighbors
	// are taken linearly and clamped at the first and last frame.
	Tag string
}

// OnionSkin renders an onion skin preview of the sprite as described
// by opts. The returned image has the same size as a single frame
// canvas. Ghost frames are composited farthest-first, and the current
// frame is always drawn last, untinted and at full opacity.
func (spr *Aseprite) OnionSkin(opts OnionSkinOptions) (*image.NRGBA, error) {
	if opts.Frame < 0 || opts.Frame >= len(spr.Frames) {
		return nil, ErrFrameOutOfRange
	}
	if opts.Opacity < 0 || opts.Opacity > 1 || opts.Falloff < 0 || opts.Falloff > 1 {
		return nil, ErrInvalidOpacity
	}

	before, after, err := spr.onionNeighbors(opts)
	if err != nil {
		return nil, err
	}

	bounds := spr.Frames[opts.Frame].Bounds
	canvas := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))

	// Draw ghost frames farthest-first so that nearer ghosts
	// and finally the current frame overlay them.
	for d := maxInt(len(before), len(after)); d >= 1; d-- {
		alpha := ghostAlpha(opts.Opacity, opts.Falloff, d)
		if alpha == 0 {
			continue
		}
		if d <= len(before) {
			drawGhost(canvas, spr.Image, spr.Frames[before[d-1]].Bounds, alpha, opts.BeforeTint)
		}
		if d <= len(after) {
			drawGhost(canvas, spr.Image, spr.Frames[after[d-1]].Bounds, alpha, opts.AfterTint)
		}
	}

	draw.Draw(canvas, canvas.Bounds(), spr.Image, bounds.Min, draw.Over)
	return canvas, nil
}

// onionNeighbors resolves the frame indexes of the before and after
// ghosts. before[0] and after[0] are the nearest neighbors.
func (spr *Aseprite) onionNeighbors(opts OnionSkinOptions) (before, after []int, err error) {
	nb := maxInt(opts.Before, 0)
	na := maxInt(opts.After, 0)

	if opts.Tag == "" {
		for d := 1; d <= nb; d++ {
			if f := opts.Frame - d; f >= 0 {
				before = append(before, f)
			}
		}
		for d := 1; d <= na; d++ {
			if f := opts.Frame + d; f < len(spr.Frames) {
				after = append(after, f)
			}
		}
		return before, after, nil
	}

	var tag *Tag
	for i := range spr.Tags {
		if spr.Tags[i].Name == opts.Tag {
			tag = &spr.Tags[i] // first match wins if names are duplicated
			break
		}
	}
	if tag == nil {
		return nil, nil, ErrTagNotFound
	}

	order := tagPlaybackOrder(*tag)
	pos := -1
	for i, f := range order {
		if f == opts.Frame {
			pos = i
			break
		}
	}
	if pos < 0 {
		return nil, nil, ErrFrameNotInTag
	}

	n := len(order)
	for d := 1; d <= nb; d++ {
		before = append(before, order[((pos-d)%n+n)%n])
	}
	for d := 1; d <= na; d++ {
		after = append(after, order[(pos+d)%n])
	}
	return before, after, nil
}

// tagPlaybackOrder returns one playback cycle of the tag as frame
// indexes, honoring its LoopDirection. PingPong endpoints are not
// duplicated within a cycle.
func tagPlaybackOrder(tag Tag) []int {
	lo, hi := int(tag.Lo), int(tag.Hi)
	if hi < lo {
		lo, hi = hi, lo
	}

	order := make([]int, 0, 2*(hi-lo+1))
	switch tag.LoopDirection {
	case Reverse:
		for i := hi; i >= lo; i-- {
			order = append(order, i)
		}
	case PingPong:
		for i := lo; i <= hi; i++ {
			order = append(order, i)
		}
		for i := hi - 1; i > lo; i-- {
			order = append(order, i)
		}
	case PingPongReverse:
		for i := hi; i >= lo; i-- {
			order = append(order, i)
		}
		for i := lo + 1; i < hi; i++ {
			order = append(order, i)
		}
	default: // Forward
		for i := lo; i <= hi; i++ {
			order = append(order, i)
		}
	}
	return order
}

// ghostAlpha converts the opacity of a ghost at distance d to an alpha byte.
func ghostAlpha(opacity, falloff float64, d int) uint8 {
	a := opacity
	for i := 1; i < d; i++ {
		a *= falloff
	}
	return uint8(a * 255)
}

// drawGhost composites the frame at srcBounds onto canvas with the
// given alpha, optionally tinted. srcBounds is the frame's bounds in
// the sprite's atlas; the frame is drawn at the canvas origin.
func drawGhost(canvas *image.NRGBA, src image.Image, srcBounds image.Rectangle, alpha uint8, tint color.Color) {
	r := canvas.Bounds().Intersect(image.Rect(0, 0, srcBounds.Dx(), srcBounds.Dy()))
	if r.Empty() {
		return
	}

	tmp := image.NewNRGBA(canvas.Bounds())
	if tint == nil {
		draw.Draw(tmp, r, src, srcBounds.Min, draw.Src)
	} else {
		t := color.NRGBAModel.Convert(tint).(color.NRGBA)
		ta := uint32(t.A)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				c := color.NRGBAModel.Convert(src.At(srcBounds.Min.X+x, srcBounds.Min.Y+y)).(color.NRGBA)
				c.R = uint8((uint32(c.R)*(255-ta) + uint32(t.R)*ta) / 255)
				c.G = uint8((uint32(c.G)*(255-ta) + uint32(t.G)*ta) / 255)
				c.B = uint8((uint32(c.B)*(255-ta) + uint32(t.B)*ta) / 255)
				tmp.SetNRGBA(x, y, c)
			}
		}
	}

	draw.DrawMask(canvas, r, tmp, image.Point{}, image.NewUniform(color.Alpha{A: alpha}), image.Point{}, draw.Over)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
