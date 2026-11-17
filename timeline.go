package aseprite

import (
	"errors"
	"math"
	"time"
)

// Errors returned by the tag playback timeline constructors and lookup entry.
// They are sentinel errors, test for them with errors.Is.
var (
	// ErrNoTags is returned when the sprite has no animation tags.
	ErrNoTags = errors.New("aseprite: sprite has no tags")

	// ErrTagNotFound is returned when the requested tag name does not exist.
	ErrTagNotFound = errors.New("aseprite: tag not found")

	// ErrDuplicateTag is returned when the requested tag name matches more
	// than one tag. Tags are allowed to share a name in the file format,
	// so the ambiguous one cannot be selected by name; construct the timeline
	// from the specific Tag with NewTimeline instead.
	ErrDuplicateTag = errors.New("aseprite: duplicate tag name")

	// ErrTagRange is returned when a tag's frame range is empty or falls
	// outside of the sprite's frame list.
	ErrTagRange = errors.New("aseprite: tag frame range is out of bounds")

	// ErrInvalidDuration is returned when a frame inside the tag range has
	// a zero or negative duration, which cannot define a playback timeline.
	ErrInvalidDuration = errors.New("aseprite: frame duration must be positive")

	// ErrDurationOverflow is returned when the accumulated playback time
	// overflows time.Duration while constructing the timeline.
	ErrDurationOverflow = errors.New("aseprite: timeline duration overflows time.Duration")

	// ErrInfiniteRepeat is returned when a tag repeats infinitely, indicated
	// by Repeat == 0. An infinite animation has no finite total duration and
	// cannot be represented as a finite timeline.
	ErrInfiniteRepeat = errors.New("aseprite: tag repeats infinitely")

	// ErrInvalidLoopDirection is returned when the tag uses a loop direction
	// other than Forward, Reverse, PingPong and PingPongReverse.
	ErrInvalidLoopDirection = errors.New("aseprite: invalid loop direction")

	// ErrNegativeTime is returned by Timeline.FrameAt when the playback time
	// is negative. Only non-negative playback times are valid.
	ErrNegativeTime = errors.New("aseprite: playback time must be non-negative")

	// ErrTimeOutOfRange is returned by Timeline.FrameAt when the playback time
	// is at or past the timeline's total duration.
	ErrTimeOutOfRange = errors.New("aseprite: playback time is past the end of the timeline")
)

// Segment is one contiguous run of frames in a tag playback timeline.
//
// Consecutive segments abut: the next segment's Start equals the previous
// segment's End, so every playback time in [Start, End) maps to FrameIndex.
// Ping-pong turnarounds share one frame, so the turn frame appears once and
// adjacent passes are merged into a single segment rather than duplicated.
type Segment struct {
	// FrameIndex is the frame index into Aseprite.Frames displayed during
	// this segment.
	FrameIndex int

	// Start is the inclusive playback time at which the segment begins.
	Start time.Duration

	// End is the exclusive playback time at which the segment ends. The
	// frame is displayed for End - Start.
	End time.Duration
}

// Timeline is a finite, time-indexed playback sequence of one tag.
//
// It is built from the sprite's Frames and a Tag and supports all four
// Aseprite loop directions: Forward, Reverse, PingPong and PingPongReverse,
// repeated a finite number of times. Segments stores every contiguous frame
// run with its start and end playback time, and FrameAt queries the frame
// displayed at any non-negative playback time.
type Timeline struct {
	// Tag is the tag that the timeline was constructed from.
	Tag Tag

	// Segments lists the playback segments in chronological order. The first
	// segment starts at zero and the last segment ends at Duration.
	Segments []Segment

	// Duration is the total playback time of the whole timeline.
	Duration time.Duration
}

// NewTimeline constructs a finite playback Timeline from frames and tag.
//
// Repeat semantics follow the Aseprite file specification: Repeat == 0 means
// unspecified and repeats forever in the editor, which is rejected with
// ErrInfiniteRepeat because it has no finite duration; Repeat == n plays the
// tag section n times. For ping-pong directions each pass counts as one play,
// so n == 1 plays one direction and n == 2 plays one pass in each direction,
// with the turnaround frame shown only once.
func NewTimeline(frames []Frame, tag Tag) (*Timeline, error) {
	n := len(frames)

	if tag.Repeat == 0 {
		return nil, ErrInfiniteRepeat
	}

	if tag.Lo > tag.Hi || int(tag.Hi) >= n {
		return nil, ErrTagRange
	}

	if tag.LoopDirection > PingPongReverse {
		return nil, ErrInvalidLoopDirection
	}

	for i := int(tag.Lo); i <= int(tag.Hi); i++ {
		if frames[i].Duration <= 0 {
			return nil, ErrInvalidDuration
		}
	}

	tl := &Timeline{Tag: tag}
	cursor := time.Duration(0)

	// appendFrame appends one frame at cursor for its full duration.
	// Consecutive segments showing the same frame (the repeated passes of a
	// single-frame range) are merged into one segment.
	appendFrame := func(frameIndex int) error {
		d := frames[frameIndex].Duration
		sum, err := addDuration(cursor, d)
		if err != nil {
			return err
		}

		if last := len(tl.Segments) - 1; last >= 0 && tl.Segments[last].FrameIndex == frameIndex {
			tl.Segments[last].End = sum
		} else {
			tl.Segments = append(tl.Segments, Segment{
				FrameIndex: frameIndex,
				Start:      cursor,
				End:        sum,
			})
		}

		cursor = sum
		return nil
	}

	multiFrame := tag.Lo != tag.Hi

	for pass := 0; pass < int(tag.Repeat); pass++ {
		lo, hi := passBounds(tag, pass)

		// A reversing ping-pong pass starts on the frame the previous pass
		// ended on. That turnaround frame is displayed once for its full
		// duration, so skip its duplicate. Single-frame ranges have no
		// turnaround and every pass counts as a full play.
		skipFirst := multiFrame && pass > 0 &&
			(tag.LoopDirection == PingPong || tag.LoopDirection == PingPongReverse)

		if forward := lo <= hi; forward {
			for frameIndex := lo; frameIndex <= hi; frameIndex++ {
				if skipFirst {
					skipFirst = false
					continue
				}
				if err := appendFrame(frameIndex); err != nil {
					return nil, err
				}
			}
		} else {
			for frameIndex := lo; frameIndex >= hi; frameIndex-- {
				if skipFirst {
					skipFirst = false
					continue
				}
				if err := appendFrame(frameIndex); err != nil {
					return nil, err
				}
			}
		}
	}

	tl.Duration = cursor
	return tl, nil
}

// passBounds returns the inclusive frame bounds visited by one pass. The
// direction alternates on every pass for ping-pong tags; Forward and Reverse
// repeat the same direction.
func passBounds(tag Tag, pass int) (lo, hi int) {
	switch tag.LoopDirection {
	case Forward:
		return int(tag.Lo), int(tag.Hi)
	case Reverse:
		return int(tag.Hi), int(tag.Lo)
	case PingPong:
		if pass%2 == 0 {
			return int(tag.Lo), int(tag.Hi)
		}
		return int(tag.Hi), int(tag.Lo)
	default: // PingPongReverse
		if pass%2 == 0 {
			return int(tag.Hi), int(tag.Lo)
		}
		return int(tag.Lo), int(tag.Hi)
	}
}

// addDuration sums two non-negative durations and rejects an overflow of
// time.Duration (maxDuration, about 290 years).
func addDuration(a, b time.Duration) (time.Duration, error) {
	if a > math.MaxInt64-b {
		return 0, ErrDurationOverflow
	}
	return a + b, nil
}

// FrameAt returns the frame index displayed at playback time t.
//
// Segments are half-open intervals, so a time exactly on a segment boundary
// selects the following frame and the result is deterministic. A negative t
// returns ErrNegativeTime and a t at or past the total duration returns
// ErrTimeOutOfRange.
func (tl *Timeline) FrameAt(t time.Duration) (int, error) {
	if t < 0 {
		return 0, ErrNegativeTime
	}

	if t >= tl.Duration {
		return 0, ErrTimeOutOfRange
	}

	// The first segment whose end is after t contains the playback time.
	lo, hi := 0, len(tl.Segments)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if tl.Segments[mid].End <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	return tl.Segments[lo].FrameIndex, nil
}

// Timeline looks up the tag named name in the sprite and builds its playback
// timeline from the sprite's frames.
//
// It returns ErrNoTags when the sprite has no tags, ErrTagNotFound when the
// name is absent and ErrDuplicateTag when the name is ambiguous. Use
// NewTimeline with the specific Tag to resolve duplicate names.
func (spr *Aseprite) Timeline(name string) (*Timeline, error) {
	if len(spr.Tags) == 0 {
		return nil, ErrNoTags
	}

	index := -1

	for i := range spr.Tags {
		if spr.Tags[i].Name == name {
			if index != -1 {
				return nil, ErrDuplicateTag
			}
			index = i
		}
	}

	if index == -1 {
		return nil, ErrTagNotFound
	}

	return NewTimeline(spr.Frames, spr.Tags[index])
}
