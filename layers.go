package aseprite

import (
	"errors"
	"image"
)

// BlendMode is the blend mode of a layer. The values match the Aseprite
// blend mode numbers used in the file format.
type BlendMode uint16

const (
	NormalBlend     BlendMode = 0
	MultiplyBlend   BlendMode = 1
	ScreenBlend     BlendMode = 2
	OverlayBlend    BlendMode = 3
	DarkenBlend     BlendMode = 4
	LightenBlend    BlendMode = 5
	ColorDodgeBlend BlendMode = 6
	ColorBurnBlend  BlendMode = 7
	HardLightBlend  BlendMode = 8
	SoftLightBlend  BlendMode = 9
	DifferenceBlend BlendMode = 10
	ExclusionBlend  BlendMode = 11
	HueBlend        BlendMode = 12
	SaturationBlend BlendMode = 13
	ColorBlend      BlendMode = 14
	LuminosityBlend BlendMode = 15
	AdditionBlend   BlendMode = 16
	SubtractBlend   BlendMode = 17
	DivideBlend     BlendMode = 18
)

// LayerType is the type of a layer.
type LayerType uint16

const (
	// NormalLayer is a regular image layer.
	NormalLayer LayerType = 0

	// GroupLayer contains other layers and has no cels of its own.
	GroupLayer LayerType = 1
)

// Layer describes a single layer of the sprite in file order.
// Every layer in the file is listed, including hidden, reference and
// group layers. Layer indices are stable and correspond to the indices
// stored in cel chunks.
type Layer struct {
	// Name is the name of the layer. Layer names are not guaranteed to be unique.
	Name string

	// Index is the index of the layer in file order.
	Index int

	// Type is the type of the layer.
	Type LayerType

	// ChildLevel is the nesting depth of the layer inside group layers.
	ChildLevel int

	// Visible reports whether the layer is visible. Hidden layers are excluded
	// from the composite image and do not get a per-layer atlas.
	Visible bool

	// Reference reports whether the layer is a reference layer. Reference
	// layers are excluded from the composite image and do not get a per-layer atlas.
	Reference bool

	// Opacity is the layer opacity in the range [0, 255].
	Opacity uint8

	// BlendMode is the blend mode of the layer.
	BlendMode BlendMode
}

// LayerFrame describes the cel of a single layer on a single frame and where
// it is stored in the layer's texture atlas.
type LayerFrame struct {
	// Bounds is the region of the layer atlas that holds the frame image.
	// The region is empty when the layer has no cel on this frame.
	Bounds image.Rectangle

	// CelBounds is the bounds of the cel in sprite (frame) coordinates.
	// The rectangle is empty when the layer has no cel on this frame.
	CelBounds image.Rectangle

	// Opacity is the combined layer and cel opacity in the range [0, 255].
	// The pixels stored in the layer atlas are painted at full cel opacity;
	// apply this value as a mask to reproduce the layer's contribution to
	// the composite image. For 16bpp grayscale cels the opacity is already
	// baked into the pixel alpha, matching the composite decoder.
	Opacity uint8

	// Linked reports whether the cel is a linked cel that shares its pixels
	// with another frame. Linked frames refer to the same atlas region as
	// the frame they link to.
	Linked bool

	// Empty reports whether the layer has no drawable cel on this frame.
	Empty bool
}

// Options configures optional decoding features. The zero value disables
// every optional feature, in which case decoding behaves exactly like Read
// and performs no extra image allocations.
type Options struct {
	// LayerAtlases enables per-layer texture atlas export. When enabled,
	// each drawable layer gets its own atlas and the layer metadata is
	// populated. Hidden, reference and group layers do not get an atlas.
	LayerAtlases bool
}

// ErrLayerNotFound is returned when a layer name or index does not exist.
var ErrLayerNotFound = errors.New("aseprite: layer not found")

// Layers returns the metadata of every layer in file order, including
// hidden, reference and group layers. It returns nil unless per-layer
// atlases were enabled through Options.
func (spr *Aseprite) Layers() []Layer {
	return spr.layers
}

// Layer returns the metadata of the layer with the given index.
func (spr *Aseprite) Layer(index int) (Layer, error) {
	if index < 0 || index >= len(spr.layers) {
		return Layer{}, ErrLayerNotFound
	}

	return spr.layers[index], nil
}

// LayerByName returns the first layer whose name matches name.
// Use LayersByName to retrieve every layer that shares a duplicate name.
func (spr *Aseprite) LayerByName(name string) (Layer, error) {
	for i := range spr.layers {
		if spr.layers[i].Name == name {
			return spr.layers[i], nil
		}
	}

	return Layer{}, ErrLayerNotFound
}

// LayersByName returns every layer whose name matches name. The returned
// slice is empty when no layer carries the name.
func (spr *Aseprite) LayersByName(name string) []Layer {
	var matches []Layer

	for i := range spr.layers {
		if spr.layers[i].Name == name {
			matches = append(matches, spr.layers[i])
		}
	}

	return matches
}

// LayerAtlas returns the texture atlas that stores every frame image of the
// layer with the given index. Frame regions are obtained with LayerFrameAt.
// Hidden, reference and group layers have no atlas, as do layers queried
// when per-layer atlases were not enabled through Options.
func (spr *Aseprite) LayerAtlas(index int) (image.Image, error) {
	if index < 0 || index >= len(spr.layerAtlases) || spr.layerAtlases[index] == nil {
		return nil, ErrLayerNotFound
	}

	return spr.layerAtlases[index], nil
}

// LayerAtlasByName returns the texture atlas of the first layer whose name
// matches name. See LayerAtlas and LayerByName.
func (spr *Aseprite) LayerAtlasByName(name string) (image.Image, error) {
	l, err := spr.LayerByName(name)
	if err != nil {
		return nil, err
	}

	return spr.LayerAtlas(l.Index)
}

// LayerFrameAt describes the cel of the given layer on the given frame.
// The returned LayerFrame always has defined values; a layer without a
// drawable cel on the frame (an empty cel, or a hidden, reference or group
// layer) is reported with an empty Bounds and Empty set to true.
func (spr *Aseprite) LayerFrameAt(layerIndex, frameIndex int) (LayerFrame, error) {
	if layerIndex < 0 || layerIndex >= len(spr.layerFrames) {
		return LayerFrame{}, ErrLayerNotFound
	}

	if frameIndex < 0 || frameIndex >= len(spr.Frames) {
		return LayerFrame{}, ErrFrameNotFound
	}

	// Hidden, reference and group layers have no descriptors: report a
	// well-defined empty frame instead of an error, since the layer exists.
	if spr.layerFrames[layerIndex] == nil {
		lf := LayerFrame{Empty: true}
		if layerIndex < len(spr.layers) {
			lf.Opacity = spr.layers[layerIndex].Opacity
		}
		return lf, nil
	}

	return spr.layerFrames[layerIndex][frameIndex], nil
}

// drawableLayer reports whether the layer contributes pixels to the
// composite image and can have a per-layer atlas.
func (f *file) drawableLayer(index int) bool {
	l := f.layers[index]
	return l.flags&1 != 0 && l.flags&64 == 0 && l.typ != 1
}
