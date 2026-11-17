package aseprite

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"sort"
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
	layer := binary.LittleEndian.Uint16(raw)
	xpos := int(binary.LittleEndian.Uint16(raw[2:]))
	ypos := int(binary.LittleEndian.Uint16(raw[4:]))
	opacity := raw[6]
	celtype := binary.LittleEndian.Uint16(raw[7:])

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

// parseSliceKey parses a single slice key from raw.
// Bounds are kept in sprite (canvas) coordinates; atlas mapping happens at
// query time because a key can be inherited by later frames.
// A truncated key yields ok=false and no key is added.
func parseSliceKey(flags uint32, raw []byte) (SliceKey, []byte, bool) {
	const (
		keyBaseLen = 20
		centerLen  = 16
		pivotLen   = 8
	)

	keyLen := keyBaseLen
	if flags&1 != 0 {
		keyLen += centerLen
	}
	if flags&2 != 0 {
		keyLen += pivotLen
	}

	if len(raw) < keyLen {
		return SliceKey{}, raw, false
	}

	var k SliceKey
	k.Frame = binary.LittleEndian.Uint32(raw)
	x := int32(binary.LittleEndian.Uint32(raw[4:]))
	y := int32(binary.LittleEndian.Uint32(raw[8:]))
	w := binary.LittleEndian.Uint32(raw[12:])
	h := binary.LittleEndian.Uint32(raw[16:])
	raw = raw[keyBaseLen:]

	k.Bounds = image.Rect(int(x), int(y), int(x)+int(w), int(y)+int(h))

	if flags&1 != 0 {
		cx := int32(binary.LittleEndian.Uint32(raw))
		cy := int32(binary.LittleEndian.Uint32(raw[4:]))
		cw := binary.LittleEndian.Uint32(raw[8:])
		ch := binary.LittleEndian.Uint32(raw[12:])
		raw = raw[centerLen:]
		k.Center = image.Rect(int(cx), int(cy), int(cx)+int(cw), int(cy)+int(ch))
	}

	if flags&2 != 0 {
		px := int32(binary.LittleEndian.Uint32(raw))
		py := int32(binary.LittleEndian.Uint32(raw[4:]))
		raw = raw[pivotLen:]
		k.Pivot = image.Pt(int(px), int(py))
	}

	return k, raw, true
}

// buildSlices collects slice chunks from every frame. Each slice chunk becomes
// one Slice whose Keys hold the raw keyframes, including their original frame
// numbers. Keys are sorted so out-of-order definitions still resolve
// deterministically. framesr maps a sprite frame to its atlas rectangle.
func (f *file) buildSlices(framesr []image.Rectangle) (slices []Slice) {
	for frameIdx := range f.frames {
		chunks := f.frames[frameIdx].chunks

		for i, chunk := range chunks {
			if chunk.typ != 0x2022 {
				continue
			}

			raw := chunk.raw

			// Truncated header: keep whatever was parsed before.
			if len(raw) < 14 {
				break
			}

			nkeys := int(binary.LittleEndian.Uint32(raw))
			flags := binary.LittleEndian.Uint32(raw[4:])
			nameLen := int(binary.LittleEndian.Uint16(raw[12:]))
			if len(raw) < 14+nameLen {
				break
			}
			name := parseString(raw[12:])
			raw = raw[14+len(name):]

			var s Slice
			s.Name = name

			for j := 0; j < nkeys && len(raw) > 0; j++ {
				var k SliceKey
				var ok bool
				if k, raw, ok = parseSliceKey(flags, raw); ok {
					s.Keys = append(s.Keys, k)
				} else {
					// Truncated key: stop, the remaining keys cannot be parsed.
					break
				}
			}

			sort.SliceStable(s.Keys, func(i, j int) bool {
				return s.Keys[i].Frame < s.Keys[j].Frame
			})

			// Static fields reflect the first key, mapped to the atlas so
			// existing single-key slice reads keep working unchanged.
			if len(s.Keys) > 0 {
				k := s.Keys[0]
				s.Center = k.Center
				s.Pivot = k.Pivot
				if int(k.Frame) < len(framesr) {
					s.Bounds = k.Bounds.Add(framesr[k.Frame].Min)
				} else {
					s.Bounds = k.Bounds
				}
			}

			// A user data chunk immediately following the slice chunk
			// belongs to the slice.
			if i < len(chunks)-1 {
				if ud := chunks[i+1]; ud.typ == 0x2020 {
					data, col := parseUserData(ud.raw)
					s.Data = append([]byte{}, data...) // copy
					s.Color = col
				}
			}

			slices = append(slices, s)
		}
	}

	return
}
