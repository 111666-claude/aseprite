package aseprite

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/askeladdk/aseprite/internal/require"
)

// sampleFrames returns three frames with distinct durations:
// frame 0 = 10ms, frame 1 = 20ms, frame 2 = 30ms. A full forward pass
// therefore lasts 60ms and every boundary time is unique.
func sampleFrames() []Frame {
	return []Frame{
		{Duration: 10 * time.Millisecond},
		{Duration: 20 * time.Millisecond},
		{Duration: 30 * time.Millisecond},
	}
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestNewTimelineDirections(t *testing.T) {
	frames := sampleFrames()

	for _, tt := range []struct {
		name      string
		direction LoopDirection
		repeat    uint16
		segments  []Segment
		duration  time.Duration
	}{
		{
			name:      "forward",
			direction: Forward,
			repeat:    2,
			segments: []Segment{
				{0, ms(0), ms(10)}, {1, ms(10), ms(30)}, {2, ms(30), ms(60)},
				{0, ms(60), ms(70)}, {1, ms(70), ms(90)}, {2, ms(90), ms(120)},
			},
			duration: ms(120),
		},
		{
			name:      "reverse",
			direction: Reverse,
			repeat:    2,
			segments: []Segment{
				{2, ms(0), ms(30)}, {1, ms(30), ms(50)}, {0, ms(50), ms(60)},
				{2, ms(60), ms(90)}, {1, ms(90), ms(110)}, {0, ms(110), ms(120)},
			},
			duration: ms(120),
		},
		{
			name:      "pingpong",
			direction: PingPong,
			repeat:    2,
			// Forward pass 0,1,2 then reverse pass 2,1,0: the turnaround
			// frame 2 is visited once, so the two passes merge into one
			// segment for frame 2 instead of repeating it.
			segments: []Segment{
				{0, ms(0), ms(10)}, {1, ms(10), ms(30)}, {2, ms(30), ms(60)},
				{1, ms(60), ms(80)}, {0, ms(80), ms(90)},
			},
			duration: ms(90),
		},
		{
			name:      "pingpongreverse",
			direction: PingPongReverse,
			repeat:    2,
			// Reverse pass 2,1,0 then forward pass 0,1,2: the turnaround
			// frame 0 is visited once.
			segments: []Segment{
				{2, ms(0), ms(30)}, {1, ms(30), ms(50)}, {0, ms(50), ms(60)},
				{1, ms(60), ms(80)}, {2, ms(80), ms(110)},
			},
			duration: ms(110),
		},
		{
			name:      "pingpong-three-passes",
			direction: PingPong,
			repeat:    3,
			// Both turnarounds (frame 2 between pass 0/1 and frame 0 between
			// pass 1/2) are shared, giving one frame per segment.
			segments: []Segment{
				{0, ms(0), ms(10)}, {1, ms(10), ms(30)}, {2, ms(30), ms(60)},
				{1, ms(60), ms(80)}, {0, ms(80), ms(90)},
				{1, ms(90), ms(110)}, {2, ms(110), ms(140)},
			},
			duration: ms(140),
		},
		{
			name:      "forward-single",
			direction: Forward,
			repeat:    1,
			segments: []Segment{
				{0, ms(0), ms(10)}, {1, ms(10), ms(30)}, {2, ms(30), ms(60)},
			},
			duration: ms(60),
		},
		{
			name:      "reverse-single",
			direction: Reverse,
			repeat:    1,
			segments: []Segment{
				{2, ms(0), ms(30)}, {1, ms(30), ms(50)}, {0, ms(50), ms(60)},
			},
			duration: ms(60),
		},
		{
			name:      "pingpong-single",
			direction: PingPong,
			repeat:    1,
			// One play is one pass in the starting direction only.
			segments: []Segment{
				{0, ms(0), ms(10)}, {1, ms(10), ms(30)}, {2, ms(30), ms(60)},
			},
			duration: ms(60),
		},
		{
			name:      "pingpongreverse-single",
			direction: PingPongReverse,
			repeat:    1,
			segments: []Segment{
				{2, ms(0), ms(30)}, {1, ms(30), ms(50)}, {0, ms(50), ms(60)},
			},
			duration: ms(60),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tag := Tag{Lo: 0, Hi: 2, Repeat: tt.repeat, LoopDirection: tt.direction}
			tl, err := NewTimeline(frames, tag)
			require.NoError(t, err)

			require.True(t, tl.Tag == tag, "tag is echoed back")
			require.True(t, tl.Duration == tt.duration, "duration", tl.Duration, tt.duration)
			require.True(t, len(tl.Segments) == len(tt.segments), "segment count", len(tl.Segments))

			var prevEnd time.Duration
			for i, want := range tt.segments {
				got := tl.Segments[i]
				require.True(t, got == want, "segment", i, got, want)
				// Segments abut and cover positive time.
				require.True(t, got.Start == prevEnd, "contiguous start", i)
				require.True(t, got.End > got.Start, "positive length", i)
				prevEnd = got.End
			}

			require.True(t, tl.Segments[0].Start == 0, "starts at zero")
			require.True(t, tl.Segments[len(tl.Segments)-1].End == tl.Duration, "ends at duration")

			// The sum of segment lengths equals the total duration.
			var sum time.Duration
			for _, seg := range tl.Segments {
				sum += seg.End - seg.Start
			}
			require.True(t, sum == tl.Duration, "segment lengths sum to duration")
		})
	}
}

func TestTimelineFrameAt(t *testing.T) {
	frames := sampleFrames()
	tag := Tag{Lo: 0, Hi: 2, Repeat: 2, LoopDirection: Forward}
	tl, err := NewTimeline(frames, tag)
	require.NoError(t, err)

	// Boundary times select the following frame (half-open intervals).
	for _, tt := range []struct {
		time  time.Duration
		frame int
	}{
		{ms(0), 0},
		{ms(9), 0},
		{ms(10), 1}, // boundary: frame 0 ends, frame 1 begins
		{ms(29), 1},
		{ms(30), 2},
		{ms(59), 2},
		{ms(60), 0}, // loop boundary restarts at frame 0
		{ms(119), 2},
	} {
		got, err := tl.FrameAt(tt.time)
		require.NoError(t, err)
		require.True(t, got == tt.frame, "time", tt.time, "frame", got, tt.frame)
	}

	// Every segment interior and both endpoints of each segment resolve to
	// the segment's frame, and every boundary resolves to the next segment.
	for i, seg := range tl.Segments {
		got, err := tl.FrameAt(seg.Start)
		require.NoError(t, err)
		require.True(t, got == seg.FrameIndex, "start of segment", i)

		mid := seg.Start + (seg.End-seg.Start)/2
		got, err = tl.FrameAt(mid)
		require.NoError(t, err)
		require.True(t, got == seg.FrameIndex, "middle of segment", i)
	}

	_, err = tl.FrameAt(-ms(1))
	require.True(t, errors.Is(err, ErrNegativeTime), "negative time", err)

	_, err = tl.FrameAt(tl.Duration)
	require.True(t, errors.Is(err, ErrTimeOutOfRange), "time at duration", err)

	_, err = tl.FrameAt(tl.Duration + ms(1))
	require.True(t, errors.Is(err, ErrTimeOutOfRange), "time past duration", err)
}

func TestNewTimelineErrors(t *testing.T) {
	frames := sampleFrames()
	base := Tag{Lo: 0, Hi: 2, Repeat: 1, LoopDirection: Forward}

	for _, tt := range []struct {
		name   string
		tag    Tag
		frames []Frame
		want   error
	}{
		{
			name: "infinite-repeat",
			tag:  Tag{Lo: 0, Hi: 2, Repeat: 0, LoopDirection: Forward},
			want: ErrInfiniteRepeat,
		},
		{
			name: "range-lo-greater-than-hi",
			tag:  Tag{Lo: 2, Hi: 1, Repeat: 1, LoopDirection: Forward},
			want: ErrTagRange,
		},
		{
			name: "range-hi-out-of-bounds",
			tag:  Tag{Lo: 0, Hi: 3, Repeat: 1, LoopDirection: Forward},
			want: ErrTagRange,
		},
		{
			name: "range-lo-out-of-bounds",
			tag:  Tag{Lo: 4, Hi: 5, Repeat: 1, LoopDirection: Forward},
			want: ErrTagRange,
		},
		{
			name:   "zero-duration",
			tag:    base,
			frames: []Frame{{Duration: 10 * time.Millisecond}, {}, {Duration: 30 * time.Millisecond}},
			want:   ErrInvalidDuration,
		},
		{
			name:   "negative-duration",
			tag:    base,
			frames: []Frame{{Duration: 10 * time.Millisecond}, {Duration: -20 * time.Millisecond}, {Duration: 30 * time.Millisecond}},
			want:   ErrInvalidDuration,
		},
		{
			name: "invalid-loop-direction",
			tag:  Tag{Lo: 0, Hi: 2, Repeat: 1, LoopDirection: 42},
			want: ErrInvalidLoopDirection,
		},
		{
			name:   "duration-overflow",
			tag:    Tag{Lo: 0, Hi: 0, Repeat: 2, LoopDirection: Forward},
			frames: []Frame{{Duration: mathMaxDuration()}},
			want:   ErrDurationOverflow,
		},
		{
			name:   "duration-overflow-reverse",
			tag:    Tag{Lo: 0, Hi: 1, Repeat: 2, LoopDirection: Reverse},
			frames: []Frame{{Duration: time.Millisecond}, {Duration: mathMaxDuration()}},
			want:   ErrDurationOverflow,
		},
		{
			name:   "duration-overflow-pingpong",
			tag:    Tag{Lo: 0, Hi: 1, Repeat: 3, LoopDirection: PingPong},
			frames: []Frame{{Duration: time.Millisecond}, {Duration: mathMaxDuration()}},
			want:   ErrDurationOverflow,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ff := frames
			if tt.frames != nil {
				ff = tt.frames
			}
			tl, err := NewTimeline(ff, tt.tag)
			require.True(t, tl == nil, "timeline must be nil on error")
			require.True(t, errors.Is(err, tt.want), "error", err, tt.want)
		})
	}
}

func mathMaxDuration() time.Duration { return time.Duration(1<<63 - 1) }

func TestNewTimelineSingleFrameRange(t *testing.T) {
	// A one-frame range never changes direction: passes visit the same frame
	// and merge into a single segment, including ping-pong turnarounds.
	frames := sampleFrames()

	for _, dir := range []LoopDirection{Forward, Reverse, PingPong, PingPongReverse} {
		tag := Tag{Lo: 1, Hi: 1, Repeat: 3, LoopDirection: dir}
		tl, err := NewTimeline(frames, tag)
		require.NoError(t, err)
		require.True(t, len(tl.Segments) == 1, "single segment", dir)
		require.True(t, tl.Segments[0].FrameIndex == 1, "frame index", dir)
		require.True(t, tl.Duration == 60*time.Millisecond, "duration", dir, tl.Duration)
	}
}

func TestAsepriteTimelineLookup(t *testing.T) {
	frames := sampleFrames()
	tag := Tag{Name: "walk", Lo: 0, Hi: 2, Repeat: 1, LoopDirection: PingPong}

	spr := &Aseprite{Frames: frames, Tags: []Tag{tag}}
	tl, err := spr.Timeline("walk")
	require.NoError(t, err)
	require.True(t, tl.Duration == ms(60), "duration", tl.Duration)

	// Identical output to constructing directly from the resolved tag.
	direct, err := NewTimeline(frames, tag)
	require.NoError(t, err)
	require.True(t, len(tl.Segments) == len(direct.Segments), "segment count")
	for i := range tl.Segments {
		require.True(t, tl.Segments[i] == direct.Segments[i], "segment", i)
	}

	// No tags at all.
	_, err = (&Aseprite{Frames: frames}).Timeline("walk")
	require.True(t, errors.Is(err, ErrNoTags), "no tags", err)

	// Missing name.
	_, err = spr.Timeline("run")
	require.True(t, errors.Is(err, ErrTagNotFound), "not found", err)

	// Duplicate names are ambiguous and cannot be selected by name.
	dup := &Aseprite{Frames: frames, Tags: []Tag{tag, tag}}
	_, err = dup.Timeline("walk")
	require.True(t, errors.Is(err, ErrDuplicateTag), "duplicate", err)

	// The duplicate is still reachable by constructing from the Tag directly.
	tl2, err := NewTimeline(frames, dup.Tags[1])
	require.NoError(t, err)
	require.True(t, tl2.Duration == ms(60), "direct construction resolves duplicates")
}

func TestAsepriteTimelineFromDecodedFile(t *testing.T) {
	// The decoded slime files contain tags with Repeat == 0, which the
	// spec defines as infinite: the lookup must surface ErrInfiniteRepeat
	// rather than constructing an unbounded timeline.
	spr := decodeSlime(t, "./testfiles/slime_paletted.aseprite")

	_, err := spr.Timeline("Down")
	require.True(t, errors.Is(err, ErrInfiniteRepeat), "infinite forward tag", err)

	_, err = spr.Timeline("Up")
	require.True(t, errors.Is(err, ErrInfiniteRepeat), "infinite ping-pong tag", err)

	// A finite copy of a decoded tag produces a working timeline and the
	// original decoding test data stays intact.
	tag := spr.Tags[1] // Down: Lo 5, Hi 9, Forward
	tag.Repeat = 1
	tl, err := NewTimeline(spr.Frames, tag)
	require.NoError(t, err)
	require.True(t, len(spr.Frames) == 10, "frame count unchanged")
	require.True(t, len(tl.Segments) == 5, "five frames in one forward pass")
	require.True(t, tl.Duration == 500*time.Millisecond, "5 frames * 100ms", tl.Duration)

	frame, err := tl.FrameAt(0)
	require.NoError(t, err)
	require.True(t, frame == 5, "first frame is Lo", frame)

	frame, err = tl.FrameAt(499 * time.Millisecond)
	require.NoError(t, err)
	require.True(t, frame == 9, "last frame is Hi", frame)
}

func decodeSlime(t *testing.T, filename string) *Aseprite {
	t.Helper()

	f, err := os.Open(filename)
	require.NoError(t, err)
	defer f.Close()

	spr, err := Read(f)
	require.NoError(t, err)
	return spr
}
