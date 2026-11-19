package aseprite

import "image"

// Mask is a binary collision mask of a single composited frame.
// It is generated from the alpha channel of the composited frame image,
// so hidden and reference layers, linked cels and blending modes are
// already accounted for. The zero value is an empty mask.
type Mask struct {
	// Bounds is the minimal bounding rectangle of all solid pixels,
	// relative to the origin of the frame the mask was generated from.
	// Bounds is the zero rectangle if no pixel passed the threshold.
	Bounds image.Rectangle

	// Pix holds the mask bits, one byte per pixel in row-major order:
	// 0 for transparent, 1 for solid. Pix is nil for an empty mask.
	Pix []byte

	// Stride is the number of bytes per row of Pix.
	Stride int
}

// Opaque reports whether the pixel at the frame-relative position (x, y)
// is solid. Positions outside Bounds, including holes in the shape,
// report false.
func (m Mask) Opaque(x, y int) bool {
	if !image.Pt(x, y).In(m.Bounds) {
		return false
	}
	return m.Pix[(y-m.Bounds.Min.Y)*m.Stride+(x-m.Bounds.Min.X)] != 0
}

// FrameMask returns the binary collision mask of frame n. A pixel is
// solid if the alpha of the composited frame image is greater than
// threshold, so a threshold of 0 marks every non-transparent pixel and
// a threshold of 255 marks nothing. The frame image is only read,
// never modified. An empty mask is returned if n is out of range.
func (spr *Aseprite) FrameMask(n int, threshold uint8) Mask {
	if n < 0 || n >= len(spr.Frames) {
		return Mask{}
	}
	return MaskFromImage(spr.Image, spr.Frames[n].Bounds, threshold)
}

// MaskFromImage builds a collision mask from the alpha channel of src
// within rectangle r. A pixel is solid if its alpha is greater than
// threshold. All coordinates in the resulting mask are relative to
// r.Min. If r does not intersect src, or no pixel passes the threshold,
// an empty mask is returned.
func MaskFromImage(src image.Image, r image.Rectangle, threshold uint8) Mask {
	r = r.Intersect(src.Bounds())
	if r.Empty() {
		return Mask{}
	}

	// Find the minimal bounding box of the solid pixels.
	minx, miny := r.Max.X, r.Max.Y
	maxx, maxy := r.Min.X, r.Min.Y
	found := false

	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if byte(a>>8) > threshold {
				found = true
				if x < minx {
					minx = x
				}
				if x > maxx {
					maxx = x
				}
				if y < miny {
					miny = y
				}
				if y > maxy {
					maxy = y
				}
			}
		}
	}

	if !found {
		return Mask{}
	}

	bounds := image.Rect(minx-r.Min.X, miny-r.Min.Y, maxx-r.Min.X+1, maxy-r.Min.Y+1)
	w := bounds.Dx()
	pix := make([]byte, w*bounds.Dy())

	for y := miny; y <= maxy; y++ {
		for x := minx; x <= maxx; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if byte(a>>8) > threshold {
				pix[(y-miny)*w+(x-minx)] = 1
			}
		}
	}

	return Mask{Bounds: bounds, Pix: pix, Stride: w}
}

// contourDirs lists the 8-neighborhood offsets in clockwise order,
// starting from the western neighbor.
var contourDirs = [8]image.Point{
	{-1, 0}, {-1, -1}, {0, -1}, {1, -1}, {1, 0}, {1, 1}, {0, 1}, {-1, 1},
}

// Contour returns the outer contour of the mask as the ordered sequence
// of boundary pixels in clockwise order, relative to the frame origin.
// The contour traces the connected component that contains the topmost
// (then leftmost) solid pixel; holes inside the shape and other
// disconnected components are not included. An isolated solid pixel
// yields a contour of that single point. An empty mask yields nil.
func (m Mask) Contour() []image.Point {
	if m.Bounds.Empty() {
		return nil
	}

	w, h := m.Bounds.Dx(), m.Bounds.Dy()

	solid := func(p image.Point) bool {
		return p.X >= 0 && p.Y >= 0 && p.X < w && p.Y < h &&
			m.Pix[p.Y*m.Stride+p.X] != 0
	}

	// nextCW returns the neighbor of p immediately clockwise from b.
	nextCW := func(p, b image.Point) image.Point {
		d := b.Sub(p)
		i := 0
		for ; i < 8; i++ {
			if contourDirs[i] == d {
				break
			}
		}
		return p.Add(contourDirs[(i+1)%8])
	}

	// scan returns the first solid neighbor of p found when scanning
	// clockwise from b, along with the background pixel just before it.
	scan := func(p, b image.Point) (c, back image.Point, ok bool) {
		for i := 0; i < 8; i++ {
			n := nextCW(p, b)
			if solid(n) {
				return n, b, true
			}
			b = n
		}
		return image.Point{}, image.Point{}, false
	}

	// The start pixel is the topmost, then leftmost, solid pixel.
	// Its western neighbor is background by construction.
	var s image.Point
find:
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if m.Pix[y*m.Stride+x] != 0 {
				s = image.Pt(x, y)
				break find
			}
		}
	}

	origin := m.Bounds.Min
	b0 := s.Add(contourDirs[0])

	c, back, ok := scan(s, b0)
	if !ok {
		// Isolated pixel.
		return []image.Point{s.Add(origin)}
	}

	// Moore-neighbor tracing with Jacob's stopping criterion:
	// stop when the start pixel would be entered again followed by
	// the same pixel as the first move.
	first := c
	contour := []image.Point{s.Add(origin)}
	p, b := c, back

	for limit := 8*w*h + 8; limit > 0; limit-- {
		contour = append(contour, p.Add(origin))
		c, back, ok = scan(p, b)
		if !ok {
			break
		}
		if c == s {
			if next, _, _ := scan(s, back); next == first {
				break
			}
		}
		p, b = c, back
	}

	return contour
}
