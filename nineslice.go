package aseprite

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
)

// Errors returned by Slice and RenderSlice.
var (
	// ErrFrameOutOfRange is returned when the frame number is negative
	// or greater than the number of frames in the sprite.
	ErrFrameOutOfRange = errors.New("aseprite: frame index out of range")

	// ErrSliceNotFound is returned when no slice key with the given name
	// applies to the given frame.
	ErrSliceNotFound = errors.New("aseprite: slice not found")

	// ErrNoNineSlice is returned when the slice has no 9-slice center.
	ErrNoNineSlice = errors.New("aseprite: slice has no 9-slice center")

	// ErrInvalidBounds is returned when the slice bounds are empty.
	ErrInvalidBounds = errors.New("aseprite: slice has empty bounds")

	// ErrInvalidCenter is returned when the 9-slice center
	// does not lie fully inside the slice bounds.
	ErrInvalidCenter = errors.New("aseprite: slice center lies outside slice bounds")

	// ErrTargetTooSmall is returned when the target size is smaller
	// than the fixed (unscalable) 9-slice borders.
	ErrTargetTooSmall = errors.New("aseprite: target size smaller than fixed 9-slice borders")
)

// Slice returns the slice key with the given name that applies to the given
// frame. A key applies to its own frame and to the frames after it, until
// the next key of a slice with the same name. If several slices share the
// same name, the key decoded first from the file wins.
//
// The returned Slice holds the Bounds, Center and Pivot for that frame.
func (spr *Aseprite) Slice(name string, frame int) (Slice, error) {
	if frame < 0 || frame >= len(spr.Frames) {
		return Slice{}, fmt.Errorf("%w: %d", ErrFrameOutOfRange, frame)
	}

	best := -1
	for i := range spr.Slices {
		s := &spr.Slices[i]
		if s.Name != name || s.Frame > frame {
			continue
		}
		if best < 0 || s.Frame > spr.Slices[best].Frame {
			best = i
		}
	}

	if best < 0 {
		return Slice{}, fmt.Errorf("%w: %q (frame %d)", ErrSliceNotFound, name, frame)
	}

	return spr.Slices[best], nil
}

// RenderSlice renders the named slice of the given frame into a new image of
// the given width and height using 9-slice scaling: the four corners are
// copied unscaled, the edges are scaled along their own axis and the center
// is scaled in both directions.
//
// The slice key that applies to the frame is resolved with Slice; its Bounds
// and Center select the source pixels in the frame's atlas image. Scaling
// interpolates in premultiplied-alpha space so that translucent borders do
// not produce dark fringes. The pivot does not take part in scaling; use
// Slice to read it.
func (spr *Aseprite) RenderSlice(name string, frame, width, height int) (*image.NRGBA, error) {
	s, err := spr.Slice(name, frame)
	if err != nil {
		return nil, err
	}

	b := s.Bounds
	if b.Empty() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidBounds, name)
	}

	c := s.Center
	if c.Empty() {
		return nil, fmt.Errorf("%w: %q", ErrNoNineSlice, name)
	}

	if c.Min.X < 0 || c.Min.Y < 0 || c.Max.X > b.Dx() || c.Max.Y > b.Dy() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidCenter, name)
	}

	left, top := c.Min.X, c.Min.Y
	right, bottom := b.Dx()-c.Max.X, b.Dy()-c.Max.Y

	if width < left+right || height < top+bottom {
		return nil, fmt.Errorf("%w: need at least %dx%d, got %dx%d",
			ErrTargetTooSmall, left+right, top+bottom, width, height)
	}

	// Copy the slice pixels from the frame's atlas image into
	// premultiplied-alpha space. Slice bounds are relative to the frame;
	// pixels outside the frame stay transparent.
	fb := spr.Frames[frame].Bounds
	atlasr := b.Add(fb.Min)
	src := premulImg{pix: make([]fcolor, b.Dx()*b.Dy()), w: b.Dx(), h: b.Dy()}
	if win := atlasr.Intersect(fb); !win.Empty() {
		ox, oy := win.Min.X-atlasr.Min.X, win.Min.Y-atlasr.Min.Y
		for y := 0; y < win.Dy(); y++ {
			for x := 0; x < win.Dx(); x++ {
				src.pix[(oy+y)*src.w+ox+x] = premulAt(spr.Image, win.Min.X+x, win.Min.Y+y)
			}
		}
	}

	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	scale9(dst, &src, left, top, right, bottom)
	return dst, nil
}

// scale9 renders src into dst using 9-slice scaling with fixed borders of
// the given sizes.
func scale9(dst *image.NRGBA, src *premulImg, left, top, right, bottom int) {
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()

	for y := 0; y < h; y++ {
		fy := axis9(y, top, bottom, src.h, h)
		for x := 0; x < w; x++ {
			fx := axis9(x, left, right, src.w, w)
			dst.SetNRGBA(x, y, bilinear(src, fx, fy))
		}
	}
}

// axis9 maps a destination coordinate to a fractional source coordinate.
// Coordinates inside the fixed borders map one-to-one; coordinates in the
// scalable center are distributed over the source center.
func axis9(d, lo, hi, srcLen, dstLen int) float64 {
	switch {
	case d < lo:
		return float64(d)
	case d >= dstLen-hi:
		return float64(srcLen-hi) + float64(d-(dstLen-hi))
	default:
		return float64(lo) + (float64(d-lo)+0.5)*float64(srcLen-lo-hi)/float64(dstLen-lo-hi) - 0.5
	}
}

// bilinear samples src at the fractional position (fx, fy), interpolating in
// premultiplied-alpha space so translucent pixels do not darken.
func bilinear(src *premulImg, fx, fy float64) color.NRGBA {
	maxx := src.w - 1
	maxy := src.h - 1

	if fx < 0 {
		fx = 0
	} else if fx > float64(maxx) {
		fx = float64(maxx)
	}
	if fy < 0 {
		fy = 0
	} else if fy > float64(maxy) {
		fy = float64(maxy)
	}

	x0, y0 := int(fx), int(fy)
	x1, y1 := x0, y0
	if x1 < maxx {
		x1++
	}
	if y1 < maxy {
		y1++
	}
	tx, ty := fx-float64(x0), fy-float64(y0)

	c0 := lerp(src.at(x0, y0), src.at(x1, y0), tx)
	c1 := lerp(src.at(x0, y1), src.at(x1, y1), tx)
	return lerp(c0, c1, ty).nrgba()
}

// premulImg is an image in premultiplied-alpha space.
type premulImg struct {
	pix []fcolor
	w   int
	h   int
}

func (p *premulImg) at(x, y int) fcolor { return p.pix[y*p.w+x] }

// fcolor is a color in premultiplied-alpha space.
type fcolor struct{ r, g, b, a float64 }

// premulAt returns the color of the pixel at (x, y)
// in premultiplied-alpha space.
func premulAt(img image.Image, x, y int) fcolor {
	r, g, b, a := img.At(x, y).RGBA()
	const s = 1.0 / 257.0
	return fcolor{float64(r) * s, float64(g) * s, float64(b) * s, float64(a) * s}
}

func lerp(a, b fcolor, t float64) fcolor {
	return fcolor{
		a.r + (b.r-a.r)*t,
		a.g + (b.g-a.g)*t,
		a.b + (b.b-a.b)*t,
		a.a + (b.a-a.a)*t,
	}
}

func (c fcolor) nrgba() color.NRGBA {
	if c.a <= 0 {
		return color.NRGBA{}
	}
	clamp := func(v float64) uint8 {
		return uint8(math.Min(math.Max(math.Round(v), 0), 255))
	}
	s := 255 / c.a
	return color.NRGBA{clamp(c.r * s), clamp(c.g * s), clamp(c.b * s), clamp(c.a)}
}
