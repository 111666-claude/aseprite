// Package aseprite implements a decoder for Aseprite sprite files.
//
// Layers are flattened, blending modes are applied,
// and frames are arranged on a single texture atlas.
// Invisible and reference layers are ignored.
// Tilesets and external files are not supported.
//
// Aseprite file format spec: https://github.com/aseprite/aseprite/blob/main/docs/ase-file-specs.md
package aseprite

import (
	"image"
	"image/color"
	"io"
	"time"
)

// LoopDirection enumerates all loop animation directions.
type LoopDirection uint8

const (
	Forward LoopDirection = iota
	Reverse
	PingPong
	PingPongReverse
)

// Tag is an animation tag.
type Tag struct {
	// Name is the name of the tag. Can be duplicate.
	Name string

	// Lo is the first frame in the animation.
	Lo uint16

	// Hi is the last frame in the animation.
	Hi uint16

	// Repeat specifies how many times to repeat the animation.
	Repeat uint16

	// LoopDirection is the looping direction of the animation.
	LoopDirection LoopDirection
}

// Frame represents a single frame in the sprite.
type Frame struct {
	// Bounds is the image bounds of the frame in the sprite's atlas.
	Bounds image.Rectangle

	// Duration is the time in seconds that the frame should be displayed for
	// in a tag animation loop.
	Duration time.Duration

	// Data lists all optional user data set in the cels that make up the frame.
	// The data of invisible and reference layers is not included.
	Data [][]byte
}

// SliceKey is a single keyframe definition of a Slice.
// A key is valid from Frame until the next key of the same slice.
type SliceKey struct {
	// Frame is the original sprite frame number (0-based) at which the key starts.
	Frame uint32

	// Bounds is the slice bounds in sprite (canvas) coordinates.
	// Use Aseprite.Slice to resolve the bounds in the texture atlas for a frame.
	Bounds image.Rectangle

	// Center is the 9-slices center relative to Bounds.
	// It is the zero rectangle when the slice has no 9-patches center.
	Center image.Rectangle

	// Pivot is the pivot point relative to Bounds.
	// It is the zero point when the slice has no pivot.
	Pivot image.Point
}

// Slice represents a single slice and its keyframes.
type Slice struct {
	// Name is the name of the slice. Can be duplicate.
	Name string

	// Keys lists the slice keyframes sorted by Frame.
	// The Bounds of a key are sprite coordinates.
	Keys []SliceKey

	// Bounds is the atlas-mapped bounds of the first keyframe.
	// This keeps static, single-key slice reads compatible.
	Bounds image.Rectangle

	// Center is the 9-slices center of the first keyframe, relative to Bounds.
	Center image.Rectangle

	// Pivot is the pivot point of the first keyframe, relative to Bounds.
	Pivot image.Point

	// Data is optional user data.
	Data []byte

	// Color is the slice color.
	Color color.Color
}

// Key returns the keyframe that is active at frame.
// A key is active from its Frame up to the next key, so the most recent key
// with Frame <= frame is used. Keys are sorted, so out-of-order definitions in
// the file are handled. ok is false when no keyframe is active at frame,
// for example when frame precedes the first keyframe.
func (s Slice) Key(frame int) (SliceKey, bool) {
	var active SliceKey
	ok := false

	for _, k := range s.Keys {
		if int(k.Frame) > frame {
			break
		}

		active = k
		ok = true
	}

	return active, ok
}

// Aseprite holds the results of a parsed Aseprite image file.
type Aseprite struct {
	// Image contains all frame images in a single image.
	// Frame bounds specify where the frame images are located.
	image.Image

	// Frames lists all frames that make up the sprite.
	Frames []Frame

	// Tags lists all animation tags.
	Tags []Tag

	// Slices lists all slices.
	Slices []Slice

	// LayerData lists the user data of all visible layers.
	LayerData [][]byte
}

func (spr *Aseprite) readFrom(r io.Reader) error {
	var f file

	if _, err := f.ReadFrom(r); err != nil {
		return err
	}

	f.initPalette()

	if err := f.initLayers(); err != nil {
		return err
	}

	if err := f.initCels(); err != nil {
		return err
	}

	var framesr []image.Rectangle
	spr.Image, framesr = f.buildAtlas()
	userdata := f.buildUserData()
	spr.Frames, userdata = f.buildFrames(framesr, userdata)
	spr.LayerData = f.buildLayerData(userdata)
	spr.Tags = f.buildTags()
	spr.Slices = f.buildSlices(framesr)
	return nil
}

// Slice finds the slice named name that is active at frame and resolves it
// against the texture atlas of that frame. Duplicate names resolve to the
// first slice with that name. The returned Bounds are mapped from sprite
// coordinates to the frame's position in the atlas; Center and Pivot stay
// relative to Bounds.
//
// ok is false when no slice with that name exists, when frame is out of
// range, or when the slice has no keyframe active at frame.
func (spr *Aseprite) Slice(name string, frame int) (Slice, bool) {
	if frame < 0 || frame >= len(spr.Frames) {
		return Slice{}, false
	}

	for i := range spr.Slices {
		s := &spr.Slices[i]
		if s.Name != name {
			continue
		}

		key, ok := s.Key(frame)
		if !ok {
			return Slice{}, false
		}

		return Slice{
			Name:   s.Name,
			Keys:   s.Keys,
			Data:   s.Data,
			Color:  s.Color,
			Bounds: key.Bounds.Add(spr.Frames[frame].Bounds.Min),
			Center: key.Center,
			Pivot:  key.Pivot,
		}, true
	}

	return Slice{}, false
}
