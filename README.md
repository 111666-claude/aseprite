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

## Tag playback timeline

A tag defines a frame range, a loop direction and a repeat count. Build a time-indexed playback timeline from the sprite's frames and tags, then look up the frame to display at any non-negative playback time:

```go
timeline, err := sprite.Timeline("walk")
if errors.Is(err, aseprite.ErrInfiniteRepeat) {
    // Repeat == 0 repeats forever in the editor and has no finite timeline.
}

frameIndex, err := timeline.FrameAt(elapsed)
frame := sprite.Frames[frameIndex]
```

`NewTimeline(frames, tag)` constructs a timeline directly from a `Tag`, which also resolves duplicate tag names. The timeline supports `Forward`, `Reverse`, `PingPong` and `PingPongReverse` with finite repeats. Each `Segment` records its frame index and half-open `[Start, End)` playback interval; ping-pong turnaround frames are shown once rather than duplicated. `FrameAt` uses binary search and returns `ErrNegativeTime` or `ErrTimeOutOfRange` for times outside the timeline.

## License

Package aseprite is released under the terms of the ISC license.

The internal blend package is released by Guillermo Estrada under the terms of the MIT license: http://github.com/phrozen/blend.
