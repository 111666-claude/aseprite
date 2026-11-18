# Aseprite image loader

[![GoDoc](https://godoc.org/github.com/askeladdk/aseprite?status.png)](https://godoc.org/github.com/askeladdk/aseprite)
[![Go Report Card](https://goreportcard.com/badge/github.com/askeladdk/aseprite)](https://goreportcard.com/report/github.com/askeladdk/aseprite)
[![Coverage Status](https://coveralls.io/repos/github/askeladdk/aseprite/badge.svg?branch=master)](https://coveralls.io/github/askeladdk/aseprite?branch=master)

## Overview

Package aseprite implements a decoder for [Aseprite sprite files](https://github.com/aseprite/aseprite/blob/main/docs/ase-file-specs.md) (`.ase` and `.aseprite` files).

Layers are flattened, blending modes are applied, and frames are arranged on a single texture atlas. Invisible and reference layers are ignored.

Limitations:
- Tilemaps are not supported.
- External files are not supported.
- Old aseprite format is not supported.
- Color profiles are ignored.

## Install

```
go get -u github.com/askeladdk/aseprite
```

## Quickstart

Use `image.Decode` to decode an aseprite sprite file to an `image.Image`:

```go
import (
    _ "github.com/askeladdk/aseprite"
)

img, imgformat, err := image.Decode("test.aseprite")
```

This is enough to decode single frame images. Multiple frames are arranged as a texture atlas in a single image. Type cast the image to `aseprite.Aseprite` to access the frame data, as well as other meta data extracted from the sprite file:

```go
if imgformat == "aseprite" {
    sprite := img.(*aseprite.Aseprite)
    for _, frame := range sprite.Frames {
        // etc ...
    }
}
```

Alternatively, use the `Read` function to directly decode an image to `aseprite.Aseprite`:

```go
sprite, err := aseprite.Read(f)
```

Read the [documentation](https://pkg.go.dev/github.com/askeladdk/aseprite) for more information about what meta data is extracted.

## Per-layer texture atlases

The flattened composite image packs every layer together. When the layers
must be exported separately (for example to package a character's body,
clothes and weapon independently), enable per-layer atlases through
`ReadWithOptions` and `Options.LayerAtlases`:

```go
sprite, err := aseprite.ReadWithOptions(f, &aseprite.Options{LayerAtlases: true})
```

Each drawable layer then gets its own texture atlas and a `LayerFrame`
descriptor per frame describing the atlas region, cel bounds, opacity and
whether the cel is linked. Layers are addressed by their stable file index
or by name:

```go
layer, err := sprite.LayerByName("weapon")
atlas, err := sprite.LayerAtlas(layer.Index)

frame, err := sprite.LayerFrameAt(layer.Index, frameIndex)
// frame.Bounds is the region of atlas holding this frame's pixels.
```

Every layer in the file is listed by `Layers`, including hidden, reference
and group layers, but only visible, non-reference, non-group layers receive
an atlas. Linked cels share their source frame's pixels, duplicate names are
resolved with the first match (`LayerByName`) or all matches
(`LayersByName`), and frames without a cel report an empty `LayerFrame`.
The composite image and all other decode results are unchanged. When the
option is left at its zero value (or `Read` is used) no per-layer images are
allocated.

## License

Package aseprite is released under the terms of the ISC license.

The internal blend package is released by Guillermo Estrada under the terms of the MIT license: http://github.com/phrozen/blend.
