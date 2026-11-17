package aseprite

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"time"

	"github.com/askeladdk/aseprite/internal/blend"
)

var errInvalidMagic = errors.New("invalid magic number")

type cel struct {
	image image.Image
	mask  image.Uniform
	data  []byte
}

func makeCelImage8(f *file, bounds image.Rectangle, opacity byte, pix []byte) cel {
	img := image.Paletted{
		Pix:     pix,
		Stride:  bounds.Dx(),
		Rect:    bounds,
		Palette: f.palette,
	}

	mask := image.Uniform{color.Alpha{opacity}}

	return cel{&img, mask, nil}
}

func makeCelImage16(f *file, bounds image.Rectangle, opacity byte, pix []byte) cel {
	img := image.NewNRGBA(bounds)

	// 16 bpp grayscale+alpha -> NRGBA
	stride := bounds.Dx() * 2
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			i := (y-bounds.Min.Y)*stride + (x-bounds.Min.X)*2
			grayValue := pix[i]    // 8-bit grey
			alphaValue := pix[i+1] // 8-bit alpha

			finalAlpha := uint16(alphaValue) * uint16(opacity) / 255

			img.SetNRGBA(x, y, color.NRGBA{
				R: grayValue,
				G: grayValue,
				B: grayValue,
				A: byte(finalAlpha),
			})
		}
	}
	mask := image.Uniform{color.Alpha{opacity}}
	return cel{img, mask, nil}
}

func makeCelImage32(f *file, bounds image.Rectangle, opacity byte, pix []byte) cel {
	img := image.NRGBA{
		Pix:    pix,
		Stride: bounds.Dx() * 4,
		Rect:   bounds,
	}

	mask := image.Uniform{color.Alpha{opacity}}

	return cel{&img, mask, nil}
}

type layer struct {
	flags     uint16
	typ       uint16
	blendMode uint16
	opacity   byte
	tileset   int // index into file.tilesets, -1 if not a tilemap layer
	data      []byte
}

func (l *layer) Parse(raw []byte) error {
	if len(raw) < 16 {
		return errors.New("aseprite: truncated layer chunk")
	}

	l.flags = binary.LittleEndian.Uint16(raw)
	l.typ = binary.LittleEndian.Uint16(raw[2:])
	l.blendMode = binary.LittleEndian.Uint16(raw[10:])
	l.opacity = raw[12]
	l.tileset = -1

	// tilemap layers reference a tileset by index
	if l.typ == 2 {
		raw = raw[16:]
		if len(raw) < 2 {
			return errors.New("aseprite: truncated layer chunk")
		}
		n := int(binary.LittleEndian.Uint16(raw)) // layer name
		raw = raw[2:]
		if len(raw) < n+4 {
			return errors.New("aseprite: truncated tilemap layer chunk")
		}
		l.tileset = int(int32(binary.LittleEndian.Uint32(raw[n:])))
	}

	return nil
}

// tileset is an embedded set of tiles referenced by tilemap layers.
type tileset struct {
	image  image.Image // tiles stacked in a vertical strip
	tilew  int
	tileh  int
	ntiles int
	empty0 bool // tile ID 0 is the empty tile
}

// makeTilesetImage converts raw tileset pixels into an image strip
// of one tile width and ntiles*tileheight height, using the file's
// color model.
func (f *file) makeTilesetImage(w, h int, pix []byte) image.Image {
	switch f.bpp {
	case 8:
		return &image.Paletted{
			Pix:     pix,
			Stride:  w,
			Rect:    image.Rect(0, 0, w, h),
			Palette: f.palette,
		}
	case 16:
		// 16 bpp grayscale+alpha -> NRGBA
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		stride := w * 2
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := y*stride + x*2
				grayValue := pix[i]
				alphaValue := pix[i+1]
				img.SetNRGBA(x, y, color.NRGBA{
					R: grayValue,
					G: grayValue,
					B: grayValue,
					A: alphaValue,
				})
			}
		}
		return img
	default:
		return &image.NRGBA{
			Pix:    pix,
			Stride: w * 4,
			Rect:   image.Rect(0, 0, w, h),
		}
	}
}

// makeTilemapCel builds a cel image from decompressed tilemap cel data.
// Each tile is looked up in the tileset and drawn at its grid position
// with its flip flags applied.
func (f *file) makeTilemapCel(ts *tileset, xpos, ypos, w, h int, opacity byte, data []byte, idMask, xMask, yMask, dMask uint32) (cel, error) {
	bounds := image.Rect(xpos, ypos, xpos+w*ts.tilew, ypos+h*ts.tileh)

	var img draw.Image
	if f.bpp == 8 {
		p := image.NewPaletted(bounds, f.palette)
		// empty tiles must stay transparent
		if f.transparent != 0 {
			for i := range p.Pix {
				p.Pix[i] = f.transparent
			}
		}
		img = p
	} else {
		img = image.NewNRGBA(bounds)
	}

	for ty := 0; ty < h; ty++ {
		for tx := 0; tx < w; tx++ {
			v := binary.LittleEndian.Uint32(data[(ty*w+tx)*4:])
			id := v & idMask

			if id == 0 && ts.empty0 {
				continue
			}

			if id >= uint32(ts.ntiles) {
				return cel{}, fmt.Errorf("aseprite: invalid tile index %d", id)
			}

			blitTile(img, ts, int(id),
				xpos+tx*ts.tilew, ypos+ty*ts.tileh,
				v&xMask != 0, v&yMask != 0, v&dMask != 0)
		}
	}

	mask := image.Uniform{color.Alpha{opacity}}

	return cel{img, mask, nil}, nil
}

// blitTile copies tile id from the tileset strip into dst at (dx, dy),
// applying the x, y and diagonal (swap x/y) flip flags.
func blitTile(dst draw.Image, ts *tileset, id, dx, dy int, fx, fy, fd bool) {
	w, h := ts.tilew, ts.tileh
	src := ts.image
	sx := src.Bounds().Min.X
	sy := src.Bounds().Min.Y + id*h

	// copy palette indices directly to preserve them exactly
	pdst, dstIsPal := dst.(*image.Paletted)
	psrc, srcIsPal := src.(*image.Paletted)
	direct := dstIsPal && srcIsPal

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			tx, ty := x, y
			if fd {
				tx, ty = ty, tx
			}
			if fx {
				tx = w - 1 - tx
			}
			if fy {
				ty = h - 1 - ty
			}
			if tx < 0 || tx >= w || ty < 0 || ty >= h {
				continue // diagonal flip of a non-square tile
			}
			if direct {
				pdst.Pix[(dy+ty-pdst.Rect.Min.Y)*pdst.Stride+dx+tx-pdst.Rect.Min.X] =
					psrc.Pix[(sy+y-psrc.Rect.Min.Y)*psrc.Stride+sx+x-psrc.Rect.Min.X]
			} else {
				dst.Set(dx+tx, dy+ty, src.At(sx+x, sy+y))
			}
		}
	}
}

type chunk struct {
	typ int
	raw []byte
}

func (c chunk) Reader() io.Reader {
	return bytes.NewReader(c.raw)
}

func (c *chunk) Read(raw []byte) ([]byte, error) {
	chunkLen := binary.LittleEndian.Uint32(raw)
	c.typ = int(binary.LittleEndian.Uint16(raw[4:]))
	c.raw = raw[6:chunkLen]
	return raw[chunkLen:], nil
}

type frame struct {
	dur    time.Duration
	chunks []chunk
	cels   []cel
}

func (f *frame) Read(raw []byte) ([]byte, error) {
	if magic := binary.LittleEndian.Uint16(raw[4:]); magic != 0xF1FA {
		return nil, errInvalidMagic
	}

	// frameLen := binary.LittleEndian.Uint32(raw[0:])
	oldChunks := binary.LittleEndian.Uint16(raw[6:])
	durationMS := binary.LittleEndian.Uint16(raw[8:])
	newChunks := binary.LittleEndian.Uint32(raw[12:])

	f.dur = time.Millisecond * time.Duration(durationMS)

	nchunks := int(newChunks)
	if nchunks == 0 {
		nchunks = int(oldChunks)
	}

	f.chunks = make([]chunk, nchunks)

	raw = raw[16:]

	for i := 0; i < nchunks; i++ {
		var c chunk
		raw, _ = c.Read(raw)
		f.chunks[i] = c
	}

	return raw, nil
}

type file struct {
	framew      int
	frameh      int
	flags       uint16
	bpp         uint16
	transparent uint8
	palette     color.Palette
	frames      []frame
	layers      []layer
	tilesets    []tileset
	makeCel     func(f *file, bounds image.Rectangle, opacity byte, pix []byte) cel
}

func (f *file) ReadFrom(r io.Reader) (int64, error) {
	var hdr [128]byte

	raw := hdr[:]

	if n, err := io.ReadFull(r, raw); err != nil {
		return int64(n), err
	}

	if magic := binary.LittleEndian.Uint16(raw[4:]); magic != 0xA5E0 {
		return 128, errInvalidMagic
	}

	if pixw, pixh := raw[34], raw[35]; pixw != pixh {
		return 128, errors.New("unsupported pixel ratio")
	}

	f.bpp = binary.LittleEndian.Uint16(raw[12:])
	f.flags = binary.LittleEndian.Uint16(raw[14:])
	f.frames = make([]frame, 0, binary.LittleEndian.Uint16(raw[6:]))
	f.framew = int(binary.LittleEndian.Uint16(raw[8:]))
	f.frameh = int(binary.LittleEndian.Uint16(raw[10:]))
	f.palette = make(color.Palette, binary.LittleEndian.Uint16(raw[32:]))
	f.transparent = raw[28]

	switch f.bpp {
	case 8:
		f.makeCel = makeCelImage8
	case 16:
		f.makeCel = makeCelImage16
	case 32:
		f.makeCel = makeCelImage32
	default:
		return 0, errors.New("invalid color depth")
	}

	for i := range f.palette {
		f.palette[i] = color.Black
	}
	f.palette[f.transparent] = color.Transparent

	fileSize := int64(binary.LittleEndian.Uint32(raw))
	raw = make([]byte, fileSize-128)

	if n, err := io.ReadFull(r, raw); err != nil {
		return int64(128 + n), err
	}

	for len(raw) > 0 {
		var fr frame
		var err error
		if raw, err = fr.Read(raw); err != nil {
			return fileSize, err
		}

		f.frames = append(f.frames, fr)
	}

	return fileSize, nil
}

func (f *file) buildAtlas() (atlas draw.Image, framesr []image.Rectangle) {
	var atlasr image.Rectangle
	atlasr, framesr = makeAtlasFrames(len(f.frames), f.framew, f.frameh)

	switch f.bpp {
	case 8:
		atlas = image.NewPaletted(atlasr, f.palette)
	case 16:
		atlas = image.NewNRGBA(atlasr)
	default:
		atlas = image.NewRGBA(atlasr)
	}

	framebounds := image.Rect(0, 0, f.framew, f.frameh)

	dstblend := image.NewRGBA(framebounds)
	dst := image.NewRGBA(framebounds)

	transparent := &image.Uniform{color.Transparent}

	for i, fr := range f.frames {
		draw.Draw(dst, framebounds, transparent, image.Point{}, draw.Src)
		for layer, c := range fr.cels {
			if c.image == nil {
				continue
			}

			src := c.image
			sr := src.Bounds()
			sp := sr.Min

			// Correction to avoid palette index errors if a color has been deleted from the Aseprite palette.
			if imgPaletted, ok := src.(*image.Paletted); ok {
				for i := range imgPaletted.Pix {
					if int(imgPaletted.Pix[i]) >= len(f.palette) {
						// Assign a transparent index if the index is outside the palette range.
						imgPaletted.Pix[i] = f.transparent
					}
				}
			}

			if mode := f.layers[layer].blendMode; mode > 0 && int(mode) < len(blend.Modes) {
				draw.Draw(dstblend, framebounds, transparent, image.Point{}, draw.Src)
				blend.Blend(dstblend, sr.Sub(sp), src, sp, dst, sp, blend.Modes[mode])
				src = dstblend
				sp = image.Point{}
			}

			draw.DrawMask(dst, sr, src, sp, &c.mask, image.Point{}, draw.Over)
		}

		draw.Draw(atlas, framesr[i], dst, image.Point{}, draw.Src)
	}

	return
}

func (f *file) buildUserData() []byte {
	n := 0

	for _, l := range f.layers {
		if l.flags&1 != 0 {
			n += len(l.data)
		}
	}

	for _, fr := range f.frames {
		for _, c := range fr.cels {
			n += len(c.data)
		}
	}

	return make([]byte, 0, n)
}

func (f *file) buildLayerData(userdata []byte) [][]byte {
	ld := make([][]byte, 0, len(f.layers))
	for _, l := range f.layers {
		if l.flags&1 != 0 && len(l.data) > 0 {
			ofs := len(userdata)
			userdata = append(userdata, l.data...)
			ld = append(ld, userdata[ofs:])
		}
	}
	return ld
}

func (f *file) buildFrames(framesr []image.Rectangle, userdata []byte) ([]Frame, []byte) {
	frames := make([]Frame, len(f.frames))

	for i, fr := range f.frames {
		frames[i].Duration = fr.dur
		frames[i].Bounds = framesr[i]
		frames[i].Data = make([][]byte, 0, len(fr.cels))
		for _, c := range fr.cels {
			if nd := len(c.data); nd > 0 {
				ofs := len(userdata)
				userdata = append(userdata, c.data...)
				frames[i].Data = append(frames[i].Data, userdata[ofs:])
			}
		}
	}

	return frames, userdata
}

func makeAtlasFrames(nframes, framew, frameh int) (atlasr image.Rectangle, framesr []image.Rectangle) {
	fw, fh := factorPowerOfTwo(nframes)
	if framew > frameh {
		fw, fh = fh, fw
	}

	atlasr = image.Rect(0, 0, fw*framew, fh*frameh)

	for i := 0; i < nframes; i++ {
		x, y := i%fw, i/fw
		framesr = append(framesr, image.Rectangle{
			Min: image.Pt(x*framew, y*frameh),
			Max: image.Pt((x+1)*framew, (y+1)*frameh),
		})
	}

	return
}

// factorPowerOfTwo computes n=a*b, where a, b are powers of two and a >= b.
func factorPowerOfTwo(n int) (a, b int) {
	x := int(math.Ceil(math.Log2(float64(n))))
	a = 1 << (x - x/2)
	b = 1 << (x / 2)
	return
}
