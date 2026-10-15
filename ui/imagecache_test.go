package ui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sync/atomic"
	"testing"
	"time"
)

// cacheJPEG is a picture of the given size, encoded the way a library's
// pictures are: decoding and halving it takes long enough for a frame budget
// to matter, which is the point of most of these tests.
func cacheJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestImageCacheDecodesOnDemand is the floor: a picture not asked for is not
// decoded, and one asked for twice is decoded once.
func TestImageCacheDecodesOnDemand(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	loads := 0
	c := &ImageCache{Picture: func(int) ([]byte, error) {
		loads++
		return encoded, nil
	}}
	c.BeginFrame()
	if held, _ := c.Held(); held != 0 {
		t.Fatalf("a cache that has drawn nothing holds %d pictures", held)
	}
	if c.Bitmap(0) == nil {
		t.Fatal("the first picture did not decode")
	}
	if loads != 1 {
		t.Fatalf("one picture loaded %d times", loads)
	}
	if c.Bitmap(0) == nil {
		t.Fatal("the second frame did not get the picture")
	}
	if loads != 1 {
		t.Fatalf("the picture was loaded again: %d loads", loads)
	}
}

// TestImageCacheFrameBudgetDefers is the first of the three decisions: a
// frame that has spent its budget hands the rest of its pictures to the next
// one rather than decoding them.
func TestImageCacheFrameBudgetDefers(t *testing.T) {
	encoded := cacheJPEG(t, 512, 512)
	c := &ImageCache{
		Picture:     func(int) ([]byte, error) { return encoded, nil },
		FrameBudget: 1, // a nanosecond: any decode spends it
	}
	c.BeginFrame()
	first := c.Bitmap(0) // the first is always taken: the check is before it
	if first == nil {
		t.Fatal("the first picture of a frame did not decode")
	}
	for i := 1; i < 5; i++ {
		if got := c.Bitmap(i); got != nil {
			t.Fatalf("picture %d decoded over a spent budget", i)
		}
	}
	if c.Deferred() != 4 {
		t.Fatalf("deferred %d pictures, want 4", c.Deferred())
	}
	// A second frame has its own budget, and decodes one more.
	c.BeginFrame()
	if c.Bitmap(1) == nil {
		t.Fatal("the next frame did not decode the picture it was left")
	}
	if c.Deferred() != 0 {
		t.Fatalf("deferred %d after decoding, want 0", c.Deferred())
	}
}

// TestImageCachePruneKeepsTheBudget is the second: the cache holds the
// screen and not the library, and drops the least recently drawn.
func TestImageCachePruneKeepsTheBudget(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64) // 16 KB a picture at 64×64
	one := 64 * 64 * 4
	c := &ImageCache{
		Picture: func(int) ([]byte, error) { return encoded, nil },
		Budget:  4 * one, // four pictures
	}
	// Five frames, one picture each: the first is the least recently
	// drawn by the time the fifth is drawn.
	for i := range 5 {
		c.BeginFrame()
		c.Bitmap(i)
		c.Prune()
	}
	held, bytes := c.Held()
	if held != 4 || bytes != 4*one {
		t.Fatalf("held %d pictures, %d bytes; want 4 and %d", held, bytes, 4*one)
	}
	c.BeginFrame()
	if c.Bitmap(0) == nil {
		t.Fatal("picture 0 should still be held")
	}
}

// TestImageCachePruneKeepsWhatTheFrameDrew is the boundary that makes the
// budget usable: a frame that draws more than the budget holds still gets all
// of it, instead of decoding and dropping the same pictures forever.
func TestImageCachePruneKeepsWhatTheFrameDrew(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	one := 64 * 64 * 4
	c := &ImageCache{
		Picture:     func(int) ([]byte, error) { return encoded, nil },
		Budget:      one, // room for one, but the frame draws four
		FrameBudget: -1,  // no cap, as a view with no budget would
	}
	c.BeginFrame()
	drawn := 0
	for i := range 4 {
		if c.Bitmap(i) != nil {
			drawn++
		}
	}
	c.Prune()
	if drawn != 4 {
		t.Fatalf("the frame drew %d of 4 pictures", drawn)
	}
	if held, _ := c.Held(); held != 4 {
		t.Fatalf("prune dropped a picture the frame drew: %d held", held)
	}
}

// TestImageCachePrefetchPreparesTheRowsPastTheScreen is the third: the
// pictures a scroll is about to ask for are held before it asks.
func TestImageCachePrefetchPreparesTheRowsPastTheScreen(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	one := 64 * 64 * 4
	// Screen: items 0..7, four to a row. Two rows past it: 8..15.
	c := &ImageCache{
		Picture:        func(int) ([]byte, error) { return encoded, nil },
		Budget:         64 * one,
		PrefetchRows:   2,
		PrefetchBudget: -1, // no pass budget: finish in one go
		View:           func() (int, int, int) { return 0, 7, 4 },
	}
	c.SetDecodeSize(64, 64)
	c.BeginFrame()
	for i := range 8 {
		c.Bitmap(i)
	}
	if got := c.Prefetch(); got != 8 {
		t.Fatalf("prefetch decoded %d pictures, want 8", got)
	}
	held, _ := c.Held()
	if held != 16 {
		t.Fatalf("held %d pictures after prefetching, want 16", held)
	}
}

// TestImageCachePrefetchNeedsRoom is why a full cache cannot prepare
// anything: a picture decoded into it is dropped by the next prune and
// decoded again, which is work with nothing to show.
func TestImageCachePrefetchNeedsRoom(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	one := 64 * 64 * 4
	c := &ImageCache{
		Picture:        func(int) ([]byte, error) { return encoded, nil },
		Budget:         8 * one,
		PrefetchRows:   2,
		PrefetchBudget: -1,
		View:           func() (int, int, int) { return 0, 7, 4 },
	}
	c.SetDecodeSize(64, 64)
	c.BeginFrame()
	for i := range 8 { // the screen fills the budget exactly
		c.Bitmap(i)
	}
	if got := c.Prefetch(); got != 0 {
		t.Fatalf("prefetch decoded %d pictures into a full cache, want 0", got)
	}
}

// TestImageCachePrefetchIsStampedBehindTheScreen is the stamping rule: a
// picture waiting for a scroll goes before one on screen when the two
// compete for the budget.
func TestImageCachePrefetchIsStampedBehindTheScreen(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	one := 64 * 64 * 4
	c := &ImageCache{
		Picture:        func(int) ([]byte, error) { return encoded, nil },
		Budget:         10 * one,
		PrefetchRows:   1,
		PrefetchBudget: -1,
		View:           func() (int, int, int) { return 0, 7, 4 },
	}
	c.SetDecodeSize(64, 64)
	c.BeginFrame()
	for i := range 8 {
		c.Bitmap(i)
	}
	c.Prefetch() // items 8..11, stamped one frame older
	c.BeginFrame()
	for i := range 8 { // drawn again: the screen
		c.Bitmap(i)
	}
	// Over budget now: prune to nine pictures, which has to lose a
	// prefetched one and not a drawn one.
	c.Budget = 9 * one
	for c.Held0() > 9 {
		if !c.PruneDropped() {
			t.Fatal("prune cannot get under the budget")
		}
	}
	c.BeginFrame()
	for i := range 8 {
		if c.Bitmap(i) == nil {
			t.Fatalf("prune dropped picture %d, which the last frame drew", i)
		}
	}
}

// Held0 is the count Held reports, for the one test that needs it alone.
func (c *ImageCache) Held0() int { n, _ := c.Held(); return n }

// PruneDropped runs one prune and reports whether it dropped anything.
func (c *ImageCache) PruneDropped() bool {
	before, _ := c.Held()
	c.Prune()
	after, _ := c.Held()
	return after < before
}

// TestImageCacheStale is the idleness rule, including the zero time: a page
// that has had no input since it started is quiet by any measure.
func TestImageCacheStale(t *testing.T) {
	c := &ImageCache{}
	cases := []struct {
		since time.Duration
		want  bool
	}{
		{0, false},                      // scrolling now
		{179 * time.Millisecond, false}, // still
		{180 * time.Millisecond, true},  // the moment after
		{5 * time.Second, true},         // long since
		{1<<62 - 1, true},               // never had input
	}
	for _, tc := range cases {
		since := tc.since
		if got := c.Stale(func() time.Duration { return since }); got != tc.want {
			t.Errorf("Stale(%v) = %v, want %v", tc.since, got, tc.want)
		}
	}
	if c.Stale(nil) {
		t.Error("Stale(nil) = true: a cache with no timeline cannot know the page is idle")
	}
}

// TestImageCacheReportsAFailedPicture: a picture that will not decode is no
// picture, and the cell draws what it is given, but the cache must not treat
// the failure as a budget spent — the frames after it still get theirs.
func TestImageCacheReportsAFailedPicture(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	c := &ImageCache{Picture: func(item int) ([]byte, error) {
		if item == 1 {
			return nil, errors.New("no such picture")
		}
		return encoded, nil
	}}
	c.BeginFrame()
	if c.Bitmap(1) != nil {
		t.Fatal("a picture that failed to load was reported as picture")
	}
	if c.Bitmap(0) == nil {
		t.Fatal("the picture after a failed one did not decode")
	}
}

// TestImageCacheNoCapDrawsEverything: a view that sets a negative
// FrameBudget gets the old behaviour, where a frame decodes all its cells.
func TestImageCacheNoCapDrawsEverything(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	c := &ImageCache{
		Picture:     func(int) ([]byte, error) { return encoded, nil },
		FrameBudget: -1,
	}
	c.BeginFrame()
	for i := range 10 {
		if c.Bitmap(i) == nil {
			t.Fatalf("picture %d was deferred with no cap set", i)
		}
	}
	if c.Deferred() != 0 {
		t.Fatalf("deferred %d with no cap set", c.Deferred())
	}
}

// mustBytes is Held's byte count, for the messages above.
func mustBytes(c *ImageCache) int { _, b := c.Held(); return b }

// TestImageCacheDecodedCountsTheFrame: a view asks the cache what the frame
// decoded rather than reading its total twice, because a total moves for
// reasons that are not this cell's (a prune drops something in between).
func TestImageCacheDecodedCountsTheFrame(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	c := &ImageCache{Picture: func(int) ([]byte, error) { return encoded, nil }}
	c.BeginFrame()
	if n, spent := c.Decoded(); n != 0 || spent != 0 {
		t.Errorf("a frame that has drawn nothing reports %d decoded in %v", n, spent)
	}
	c.Bitmap(0)
	c.Bitmap(0) // held already: not a decode
	c.Bitmap(1)
	if n, _ := c.Decoded(); n != 2 {
		t.Errorf("the frame decoded %d, want 2", n)
	}
	// spent is the frame's own decoding time, and a 64×64 picture decodes
	// inside the clock's resolution: what is asserted is that it is not
	// negative and that a frame which decoded reports the count.
	if _, spent := c.Decoded(); spent < 0 {
		t.Errorf("the frame reports %v spent decoding", spent)
	}
	c.BeginFrame()
	if n, _ := c.Decoded(); n != 0 {
		t.Errorf("a new frame reports the last one's %d decodes", n)
	}
}

// TestQuietSince is the whole reason QuietSince exists: the framework reports
// the zero time for a page that has had no event, and time.Since of the year
// one is a duration of five thousand years that overflows into a negative —
// which would read as "someone is using the page" forever.
func TestQuietSince(t *testing.T) {
	never := QuietSince(func() time.Time { return time.Time{} })
	if got := never(); got < time.Hour {
		t.Errorf("a page with no input reports %v since it, want a duration that counts as quiet", got)
	}
	if got := never(); got < 0 {
		t.Errorf("the zero time reads as %v: the overflow the doc warns about", got)
	}
	now := time.Now()
	recent := QuietSince(func() time.Time { return now })
	if got := recent(); got < 0 || got > time.Minute {
		t.Errorf("an event just now reports %v since it, want about zero", got)
	}
	c := &ImageCache{}
	if !c.Stale(never) {
		t.Error("a page that has never had an event is not quiet by Stale")
	}
	if c.Stale(recent) {
		t.Error("a page used just now is quiet by Stale")
	}
}

// TestImageCacheTrackCell: the cache takes the size of the grid's cells, and
// leaves the size alone while the grid has measured nothing.
func TestImageCacheTrackCell(t *testing.T) {
	s := GridState{}
	c := &ImageCache{}
	c.TrackCell(&s, 1)
	if c.DecodeW != 0 {
		t.Errorf("a grid that has not been laid out set the decode width to %d", c.DecodeW)
	}
	coreNewTester(func(ctx *context) {
		coreColumn(ctx).Fill().Children(func() {
			coreGridView(ctx, &s, 100, 100, 400, func(int) {}).Grow(1)
		})
	}, 300, 1000)
	c.TrackCell(&s, 1)
	w, h, _ := s.CellSize()
	if c.DecodeW == 0 || c.DecodeH == 0 {
		t.Fatalf("a laid-out grid left the decode size at %dx%d", c.DecodeW, c.DecodeH)
	}
	if float32(c.DecodeW) < w || float32(c.DecodeH) < h {
		t.Errorf("decoding at %dx%d, smaller than the %gx%g cell it shows in", c.DecodeW, c.DecodeH, w, h)
	}
	if c.Room != c.DecodeW*c.DecodeH*4 {
		t.Errorf("a picture of that size is %d bytes, but Room is %d", c.DecodeW*c.DecodeH*4, c.Room)
	}
}

// TestImageCacheAsyncWaitsThenDraws: a source that cannot answer where it is
// asked gives the frame a placeholder and the frame after the bytes arrive
// the picture. The frame does not wait, and the item is asked for once
// however many frames want it in between.
func TestImageCacheAsyncWaitsThenDraws(t *testing.T) {
	encoded := cacheJPEG(t, 64, 64)
	var asked, wants atomic.Int64
	var answer atomic.Value // func([]byte, error)
	c := &ImageCache{
		PictureAsync: &ImageCacheAsync{
			Bytes: func(item int, done func([]byte, error)) {
				asked.Add(1)
				answer.Store(done)
			},
			Want: func(int) { wants.Add(1) },
		},
	}
	c.BeginFrame()
	if c.Bitmap(0) != nil {
		t.Fatal("a frame drew a picture whose bytes are not here")
	}
	if asked.Load() != 1 {
		t.Fatalf("the source was asked %d times for one picture, want 1", asked.Load())
	}
	// A frame that leaves a picture to the next one has to say so, or the
	// view asks for no next frame and the bytes that arrive are drawn by
	// nobody: Deferred is what the view's Context.Invalidate hangs on.
	if c.Deferred() == 0 {
		t.Fatal("a frame waiting for bytes left nothing to the next frame, but nothing would ask for it")
	}
	// Frames that want it while it is on its way ask again for nothing.
	for range 5 {
		c.BeginFrame()
		c.Bitmap(0)
	}
	if asked.Load() != 1 {
		t.Errorf("the source was asked %d times while one picture was on its way, want 1", asked.Load())
	}
	answer.Load().(func([]byte, error))(encoded, nil)
	c.BeginFrame()
	bm := c.Bitmap(0)
	if bm == nil {
		t.Fatal("the frame after the bytes arrived did not draw the picture")
	}
	if w, h := bm.Size(); w != 64 || h != 64 {
		t.Errorf("the picture is %dx%d, want 64x64", w, h)
	}
	// And a frame that drew everything asks for nothing more, which is what
	// ends the run of frames a view asks for.
	if n := c.Deferred(); n != 0 {
		t.Errorf("the frame that drew the picture left %d to the next one, want 0", n)
	}
}

// TestImageCacheAsyncFailureIsRemembered is the failure a list has to
// survive: a source that answered with nothing is not asked again. Asking
// again every frame is one request per frame for as long as the picture is on
// screen, which turns one broken URL into a page's worth of traffic; the item
// is the view's to retry if it wants (Forget).
func TestImageCacheAsyncFailureIsRemembered(t *testing.T) {
	var answer func([]byte, error)
	var asked atomic.Int64
	c := &ImageCache{PictureAsync: &ImageCacheAsync{
		Bytes: func(_ int, done func([]byte, error)) { asked.Add(1); answer = done },
	}}
	c.BeginFrame()
	c.Bitmap(0)
	answer(nil, errors.New("no"))
	if !c.Failed(0) {
		t.Error("a failed picture is not reported as failed")
	}
	for range 5 {
		c.BeginFrame()
		c.Bitmap(0)
	}
	if asked.Load() != 1 {
		t.Errorf("a failed source was asked %d times over six frames, want 1", asked.Load())
	}
	// And the retry a view offers: Forget drops the failure and it asks once
	// more, which is how a "try again" works.
	c.Forget(0)
	c.BeginFrame()
	c.Bitmap(0)
	if asked.Load() != 2 {
		t.Errorf("after Forget the source was asked %d times, want 2", asked.Load())
	}
	if c.Failed(0) {
		t.Error("Forget did not clear the failure")
	}
}

// TestImageCacheAsyncFailureIsNotAWait: a failure and a picture on its way
// are both nil from Bitmap and are not the same thing to a view — one is
// coming and the other is not — which is what Failed is for.
func TestImageCacheAsyncFailureIsNotAWait(t *testing.T) {
	var answer func([]byte, error)
	c := &ImageCache{PictureAsync: &ImageCacheAsync{
		Bytes: func(_ int, done func([]byte, error)) { answer = done },
	}}
	c.BeginFrame()
	c.Bitmap(0)
	if c.Failed(0) {
		t.Error("a picture asked for and not yet answered is reported as failed")
	}
	answer(nil, errors.New("no"))
	if !c.Failed(0) {
		t.Error("a picture that answered with nothing is not reported as failed")
	}
}

// TestImageCacheFetchAsksAheadOfTheFrame is Want's purpose: the bytes of the
// rows past the screen are asked for in the quiet moment, so that the frame
// that scrolls to them has something to decode and does not stop to fetch.
func TestImageCacheFetchAsksAheadOfTheFrame(t *testing.T) {
	var wants []int
	c := &ImageCache{
		View:         func() (int, int, int) { return 0, 3, 3 }, // one row of three
		PrefetchRows: 1,
		PictureAsync: &ImageCacheAsync{
			Bytes: func(int, func([]byte, error)) {},
			Want:  func(i int) { wants = append(wants, i) },
		},
	}
	if n := c.Fetch(); n != 3 {
		t.Errorf("Fetch asked for %d items past a screen of three, want 3", n)
	}
	if len(wants) != 3 || wants[0] != 4 || wants[2] != 6 {
		t.Errorf("Want was told %v, want the row after the screen: 4,5,6", wants)
	}
	// Asked for already: a second pass has nothing new to do.
	if n := c.Fetch(); n != 0 {
		t.Errorf("a second Fetch asked for %d more", n)
	}
	// A source that answers where it is asked has nothing to fetch.
	c.PictureAsync = nil
	c.Picture = func(int) ([]byte, error) { return nil, nil }
	if n := c.Fetch(); n != 0 {
		t.Errorf("a synchronous source was fetched %d times", n)
	}
}

// TestImageCacheAsyncConcurrent is a race check, and it is the real one: the
// answer comes on the goroutine the fetch runs on — under wasm the browser's
// callback, off it whatever the source uses — while the frame that asked runs
// on the main one. Two frames at once is not a case (a frame is one
// goroutine), so the frame here is a single goroutine and the answers are the
// others, which is what the async path is for. Run with -race.
func TestImageCacheAsyncConcurrent(t *testing.T) {
	encoded := cacheJPEG(t, 32, 32)
	answered := make(chan struct{}, 64)
	c := &ImageCache{PictureAsync: &ImageCacheAsync{
		Bytes: func(_ int, done func([]byte, error)) {
			go func() { done(encoded, nil); answered <- struct{}{} }()
		},
	}}
	c.SetDecodeSize(32, 32)
	// The frame, on this goroutine: draw eight cells, settle, repeat. Each
	// pass draws more of them as answers land, and the ones drawn are held.
	for f := 0; f < 100; f++ {
		c.BeginFrame()
		for i := range 8 {
			c.Bitmap(i)
		}
		c.Prune()
		// Let some answers land between frames, which is what happens
		// while a page is quiet: the fetch is not the frame's goroutine.
		if f%5 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	// One more frame after everything has been answered.
	deadline := time.After(2 * time.Second)
	for {
		n, _ := c.Held()
		if n == 8 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("100 frames drew %d of the 8 pictures; the answers were not decoded", n)
		case <-time.After(time.Millisecond):
		}
	}
	if n, bytes := c.Held(); bytes != n*32*32*4 {
		t.Errorf("the cache says it holds %d bytes for %d pictures of 4096", bytes, n)
	}
}
