package aseprite

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

func skipString(raw []byte) []byte {
	n := binary.LittleEndian.Uint16(raw)
	return raw[2+n:]
}

func parseString(raw []byte) string {
	n := binary.LittleEndian.Uint16(raw)
	return string(raw[2 : 2+n])
}

func parseColor(raw []byte) color.Color {
	return color.NRGBA{
		R: raw[0],
		G: raw[1],
		B: raw[2],
		A: raw[3],
	}
}

func parseUserData(raw []byte) (data []byte, color color.Color) {
	flags := binary.LittleEndian.Uint32(raw)
	raw = raw[4:]

	if flags&1 != 0 {
		n := binary.LittleEndian.Uint16(raw)
		data, raw = raw[2:2+n], raw[2+n:]
	}

	if flags&2 != 0 {
		color = parseColor(raw)
	}

	return
}

func (f *file) parseChunk2019(raw []byte) {
	entries := binary.LittleEndian.Uint32(raw[0:])
	lo := binary.LittleEndian.Uint32(raw[4:])

	raw = raw[20:]

	for i := uint32(0); i < entries; i++ {
		flags := binary.LittleEndian.Uint16(raw)
		f.palette[lo+i] = parseColor(raw[2:])
		raw = raw[6:]

		if flags&1 != 0 {
			raw = skipString(raw)
		}
	}
}

// https://github.com/aseprite/aseprite/blob/main/docs/ase-file-specs.md#old-palette-chunk-0x0011
func (f *file) parseChunk0011(raw []byte) {
	packets := binary.LittleEndian.Uint16(raw)
	raw = raw[2:]

	currentIndex := 0

	for i := 0; i < int(packets); i++ {
		skip := int(raw[0])
		currentIndex += skip

		n := int(raw[1])
		if n == 0 {
			n = 256
		}
		raw = raw[2:]

		for j := 0; j < n && currentIndex < len(f.palette); j++ {
			f.palette[currentIndex] = color.NRGBA{
				R: raw[0] * 4,
				G: raw[1] * 4,
				B: raw[2] * 4,
				A: 255,
			}
			raw = raw[3:]
			currentIndex++
		}
	}
}

// https://github.com/aseprite/aseprite/blob/main/docs/ase-file-specs.md#old-palette-chunk-0x0004
func (f *file) parseChunk0004(raw []byte) {
	packets := binary.LittleEndian.Uint16(raw)
	raw = raw[2:]

	currentIndex := 0

	for i := 0; i < int(packets); i++ {
		skip := int(raw[0])
		currentIndex += skip

		n := int(raw[1])
		if n == 0 {
			n = 256
		}
		raw = raw[2:]

		for j := 0; j < n && currentIndex < len(f.palette); j++ {
			f.palette[currentIndex] = color.NRGBA{
				R: raw[0],
				G: raw[1],
				B: raw[2],
				A: 255,
			}
			raw = raw[3:]
			currentIndex++
		}
	}
}

func (f *file) initPalette() {
	var chunk0004 []byte
	var chunk0011 []byte
	found2019 := false

	for _, ch := range f.frames[0].chunks {
		if ch.typ == 0x2019 {
			f.parseChunk2019(ch.raw)
			found2019 = true
			break
		}
		if ch.typ == 0x0004 {
			chunk0004 = ch.raw
		}
		if ch.typ == 0x0011 {
			chunk0011 = ch.raw
		}
	}

	if !found2019 {
		if chunk0004 != nil {
			f.parseChunk0004(chunk0004)
		} else if chunk0011 != nil {
			f.parseChunk0011(chunk0011)
		}
	}

	if f.flags&1 != 0 {
		f.palette[f.transparent] = color.Transparent
	}
}

// https://github.com/aseprite/aseprite/blob/main/docs/ase-file-specs.md#tileset-chunk-0x2023
func (f *file) parseChunk2023(raw []byte) (tileset, error) {
	var ts tileset

	if len(raw) < 32 {
		return ts, errors.New("aseprite: truncated tileset chunk")
	}

	flags := binary.LittleEndian.Uint32(raw[4:])
	ntiles := int(binary.LittleEndian.Uint32(raw[8:]))
	tilew := int(binary.LittleEndian.Uint16(raw[12:]))
	tileh := int(binary.LittleEndian.Uint16(raw[14:]))

	if flags&1 != 0 {
		return ts, errors.New("aseprite: external tilesets are not supported")
	}

	if flags&2 == 0 {
		return ts, errors.New("aseprite: tileset has no embedded tiles")
	}

	if tilew <= 0 || tileh <= 0 || ntiles <= 0 {
		return ts, errors.New("aseprite: invalid tileset dimensions")
	}

	// skip base index and reserved bytes, then the tileset name
	raw = raw[32:]
	if len(raw) < 2 {
		return ts, errors.New("aseprite: truncated tileset chunk")
	}
	n := int(binary.LittleEndian.Uint16(raw))
	raw = raw[2:]
	if len(raw) < n+4 {
		return ts, errors.New("aseprite: truncated tileset chunk")
	}
	raw = raw[n:]

	zlen := int(binary.LittleEndian.Uint32(raw))
	raw = raw[4:]
	if len(raw) < zlen {
		return ts, errors.New("aseprite: truncated tileset data")
	}

	zr, err := zlib.NewReader(bytes.NewReader(raw[:zlen]))
	if err != nil {
		return ts, err
	}
	pix, err := io.ReadAll(zr)
	if err != nil {
		return ts, err
	}

	want := int64(tilew) * int64(tileh) * int64(ntiles) * int64(f.bpp) / 8
	if int64(len(pix)) != want {
		return ts, fmt.Errorf("aseprite: unexpected tileset data size %d, want %d", len(pix), want)
	}

	ts.image = f.makeTilesetImage(tilew, tileh*ntiles, pix)
	ts.tilew = tilew
	ts.tileh = tileh
	ts.ntiles = ntiles
	ts.empty0 = flags&4 != 0

	return ts, nil
}

func (f *file) initTilesets() error {
	for i := range f.frames {
		for _, ch := range f.frames[i].chunks {
			if ch.typ == 0x2023 {
				ts, err := f.parseChunk2023(ch.raw)
				if err != nil {
					return err
				}
				f.tilesets = append(f.tilesets, ts)
			}
		}
	}

	return nil
}

func (f *file) initLayers() error {
	chunks := f.frames[0].chunks
	for i, ch := range chunks {
		if ch.typ == 0x2004 {
			var l layer
			if err := l.Parse(ch.raw); err != nil {
				return err
			}

			if i < len(chunks)-1 {
				if ch2 := chunks[i+1]; ch2.typ == 0x2020 {
					l.data, _ = parseUserData(ch2.raw)
				}
			}

			f.layers = append(f.layers, l)
		}
	}

	nlayers := len(f.layers)
	for i := range f.frames {
		f.frames[i].cels = make([]cel, nlayers)
	}

	return nil
}

func (f *file) parseChunk2005(frame int, raw []byte) (*cel, error) {
	layer := int(binary.LittleEndian.Uint16(raw))
	xpos := int(binary.LittleEndian.Uint16(raw[2:]))
	ypos := int(binary.LittleEndian.Uint16(raw[4:]))
	opacity := raw[6]
	celtype := binary.LittleEndian.Uint16(raw[7:])

	if layer >= len(f.layers) {
		return nil, fmt.Errorf("aseprite: cel references invalid layer index %d", layer)
	}

	// invisible layer
	if f.layers[layer].flags&1 == 0 {
		return nil, nil
	}

	// reference layer
	if f.layers[layer].flags&64 != 0 {
		return nil, nil
	}

	raw = raw[16:]

	opacity = byte((int(opacity) * int(f.layers[layer].opacity)) / 255)

	switch celtype {
	case 0: // uncompressed image
		width := int(binary.LittleEndian.Uint16(raw))
		height := int(binary.LittleEndian.Uint16(raw[2:]))
		pix := raw[4:]
		bounds := image.Rect(xpos, ypos, xpos+width, ypos+height)
		cel := f.makeCel(f, bounds, opacity, pix)
		f.frames[frame].cels[layer] = cel
	case 1: // linked cel
		srcFrame := int(binary.LittleEndian.Uint16(raw))
		srcCel := f.frames[srcFrame].cels[layer]
		f.frames[frame].cels[layer] = srcCel
	case 2: // compressed image
		width := int(binary.LittleEndian.Uint16(raw))
		height := int(binary.LittleEndian.Uint16(raw[2:]))
		zr, err := zlib.NewReader(bytes.NewReader(raw[4:]))
		if err != nil {
			return nil, err
		}
		pix, err := io.ReadAll(zr)
		if err != nil {
			return nil, err
		}
		bounds := image.Rect(xpos, ypos, xpos+width, ypos+height)
		cel := f.makeCel(f, bounds, opacity, pix)
		f.frames[frame].cels[layer] = cel
	case 3: // compressed tilemap
		l := f.layers[layer]

		if l.typ != 2 {
			return nil, errors.New("aseprite: tilemap cel on non-tilemap layer")
		}

		if l.tileset < 0 || l.tileset >= len(f.tilesets) {
			return nil, fmt.Errorf("aseprite: layer references invalid tileset index %d", l.tileset)
		}

		if len(raw) < 32 {
			return nil, errors.New("aseprite: truncated tilemap cel")
		}

		width := int(binary.LittleEndian.Uint16(raw))
		height := int(binary.LittleEndian.Uint16(raw[2:]))

		if width <= 0 || height <= 0 {
			return nil, errors.New("aseprite: invalid tilemap dimensions")
		}

		if bpt := binary.LittleEndian.Uint16(raw[4:]); bpt != 32 {
			return nil, fmt.Errorf("aseprite: unsupported %d bits per tile", bpt)
		}

		idMask := binary.LittleEndian.Uint32(raw[6:])
		xMask := binary.LittleEndian.Uint32(raw[10:])
		yMask := binary.LittleEndian.Uint32(raw[14:])
		dMask := binary.LittleEndian.Uint32(raw[18:])

		zr, err := zlib.NewReader(bytes.NewReader(raw[32:]))
		if err != nil {
			return nil, err
		}
		pix, err := io.ReadAll(zr)
		if err != nil {
			return nil, err
		}

		if want := width * height * 4; len(pix) != want {
			return nil, fmt.Errorf("aseprite: unexpected tilemap data size %d, want %d", len(pix), want)
		}

		ts := &f.tilesets[l.tileset]
		cel, err := f.makeTilemapCel(ts, xpos, ypos, width, height, opacity, pix, idMask, xMask, yMask, dMask)
		if err != nil {
			return nil, err
		}
		f.frames[frame].cels[layer] = cel
	default:
		return nil, errors.New("unsupported cel type")
	}

	return &f.frames[frame].cels[layer], nil
}

func (f *file) initCels() error {
	for i := range f.frames {
		chunks := f.frames[i].chunks
		for j, ch := range chunks {
			if ch.typ == 0x2005 {
				cel, err := f.parseChunk2005(i, ch.raw)
				if err != nil {
					return err
				} else if cel != nil && j < (len(chunks)-1) {
					// user data chunk
					if ch2 := chunks[j+1]; ch2.typ == 0x2020 {
						cel.data, _ = parseUserData(ch2.raw)
					}
				}
			}
		}
	}

	return nil
}

func parseTag(t *Tag, raw []byte) []byte {
	t.Lo = binary.LittleEndian.Uint16(raw)
	t.Hi = binary.LittleEndian.Uint16(raw[2:])
	t.LoopDirection = LoopDirection(raw[4])
	t.Repeat = binary.LittleEndian.Uint16(raw[5:])
	t.Name = parseString(raw[17:])
	return raw[19+len(t.Name):]
}

func (f *file) buildTags() []Tag {
	for _, chunk := range f.frames[0].chunks {
		if chunk.typ == 0x2018 {
			raw := chunk.raw
			ntags := binary.LittleEndian.Uint16(raw)
			tags := make([]Tag, ntags)
			raw = raw[10:]
			for i := range tags {
				raw = parseTag(&tags[i], raw)
			}
			return tags
		}
	}

	return nil
}

func parseSlice(s *Slice, flags uint32, raw []byte) []byte {
	// framenum := binary.LittleEndian.Uint32(raw)
	x := int32(binary.LittleEndian.Uint32(raw[4:]))
	y := int32(binary.LittleEndian.Uint32(raw[8:]))
	w := binary.LittleEndian.Uint32(raw[12:])
	h := binary.LittleEndian.Uint32(raw[16:])
	raw = raw[20:]

	var cx, cy int32
	var cw, ch uint32

	if flags&1 != 0 {
		cx = int32(binary.LittleEndian.Uint32(raw))
		cy = int32(binary.LittleEndian.Uint32(raw[4:]))
		cw = binary.LittleEndian.Uint32(raw[8:])
		ch = binary.LittleEndian.Uint32(raw[12:])
		raw = raw[16:]
	}

	var px, py int32

	if flags&2 != 0 {
		px = int32(binary.LittleEndian.Uint32(raw))
		py = int32(binary.LittleEndian.Uint32(raw[4:]))
		raw = raw[8:]
	}

	s.Bounds = image.Rect(int(x), int(y), int(x)+int(w), int(y)+int(h))
	s.Center = image.Rect(int(cx), int(cy), int(cx)+int(cw), int(cy)+int(ch))
	s.Pivot = image.Pt(int(px), int(py))

	return raw
}

func (f *file) buildSlices() (slices []Slice) {
	chunks := f.frames[0].chunks
	for i, chunk := range chunks {
		if chunk.typ == 0x2022 {
			ofs := len(slices)
			raw := chunk.raw
			nslices := int(binary.LittleEndian.Uint32(raw))
			flags := binary.LittleEndian.Uint32(raw[4:])
			name := parseString(raw[12:])

			// parse each slice
			raw = raw[14+len(name):]
			for i := 0; len(raw) > 0 && i < nslices; i++ {
				var s Slice
				s.Name = name
				raw = parseSlice(&s, flags, raw)
				slices = append(slices, s)
			}

			// check for user data chunk
			if i < len(chunks)-1 {
				if ud := chunks[i+1]; ud.typ == 0x2020 {
					data, col := parseUserData(ud.raw)
					data = append([]byte{}, data...) // copy
					for j := ofs; j < len(slices); j++ {
						slices[j].Data = data
						slices[j].Color = col
					}
				}
			}
		}
	}

	return
}
