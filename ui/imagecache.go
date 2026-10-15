package ui

import (
	"cmp"
	"errors"
	"slices"
	"sync"
	"time"
)

// ImageCache holds a picture list's decoded pictures at the size they show
// at, and makes the three decisions a list of more pictures than fit has to
// make: what to decode and when, what to keep, and what to prepare before it
// is asked for.
//
// A view of a library of any size asks it for the picture of a cell as the
// cell is built, so a library of a hundred thousand costs a screenful's
// worth and answers in a screenful's time. The alternatives all measure
// worse: decoding a screenful in the frame that scrolls it in takes
// seventeen pictures' worth of time and drops every frame while it runs
// (§9.16: 117 ms, thirty-three frames); a cache without a budget grows with
// the library until a wasm32's 4 GB of address space runs out at about eight
// thousand pictures (§9.14); and a picture decoded in the frame that draws
// it drops that frame however the decoding is scheduled, so a scroll of a
// screenful drops none only if the pictures were held before it started.
//
// Three answers, one type:
//
//   - [ImageCache.Picture] decodes within FrameBudget, and returns nothing
//     once the frame has spent it, for the cell to draw as it finds;
//   - [ImageCache.Prune] keeps the total within Budget, dropping the least
//     recently drawn;
//   - [ImageCache.Prefetch] decodes the rows past the screen in the moments
//     nobody is using the page.
//
// None of the three is free to get right, and none is a view's to invent:
// the numbers are in those methods.
type ImageCache struct {
	// Budget is how many bytes of decoded pictures are held between them,
	// or zero for 16 MB, which is about a hundred and twenty cells of the
	// size a library shows them at against the sixty or so a window
	// shows.
	//
	// It is bytes rather than pictures because bytes are what has to fit:
	// a hundred thumbnails and one photograph of the same total cost the
	// same. Chrome's ImageDecodeCache holds decoded pixels and their
	// textures up to about 128 MB on a phone; Flutter's ImageCache
	// defaults to 100 MB. Both evict the least recently drawn, and both
	// are the reference for this.
	Budget int

	// FrameBudget is how long one frame may spend decoding, or zero for
	// 3 ms. A frame that has spent it draws the rest of its cells as it
	// finds them and leaves their pictures for the next frame, so the
	// only way to hold a frame within a refresh is to decide, inside the
	// frame, to stop.
	//
	// It is against the frame's own time: at 60 Hz a frame has 16.7 ms,
	// and at the 180 Hz of the screens this was measured on, 5.55 ms.
	// Zero as a value means no cap, which is the default, so pass a
	// negative duration for "no cap" where that is wanted.
	FrameBudget time.Duration

	// PrefetchRows is how many rows past the ones on screen Prefetch
	// works on, or zero for 2. PrefetchBudget is how long one pass of it
	// may take, or zero for 8 ms — larger than a frame's, because a pass
	// has no frame to drop.
	PrefetchRows   int
	PrefetchBudget time.Duration

	// PollEvery is how often Run looks for work to do ahead, or zero for a
	// tenth of a second.
	PollEvery time.Duration

	// Past is how long the page may have gone without input for a moment
	// to count as nobody's, or zero for 180 ms. It is what Stale reads, and
	// the caller supplies the time itself (see QuietTimeline), since whether
	// anyone is using the page is the framework's to know and not the
	// cache's.
	Past time.Duration

	// View says which items the view built last frame and how many fit in
	// a row: first through last inclusive, last -1 for none, and the
	// columns. It is the one thing the cache cannot work out for itself —
	// what is on screen is the view's — and a view that does not set it
	// prefetches nothing.
	View func() (first, last, cols int)

	// Picture is what one item's picture is decoded from. It is called
	// with the item and returns its encoded bytes, or an error, which the
	// cache reports as no picture: where the bytes come from — an embed,
	// a file, a fetch — is the view's and not the cache's.
	//
	// It is for sources that answer now: a file, an embed, a buffer. A
	// source that has to go somewhere for its bytes — the network, a
	// pipe, a database — cannot answer here, and answering late is not
	// something a caller can do, so it takes PictureAsync instead.
	Picture func(item int) ([]byte, error)

	// PictureAsync is where the bytes of an item come from where they are
	// not here yet, for a view that fetches them (ImageCache.Fetch):
	//
	//	c.Async = &ImageCacheAsync{
	//		Bytes: func(item int, done func([]byte, error)) {
	//			go func() { done(get(item)) }()
	//		},
	//		Want: func(item int) {},
	//	}
	//
	// Bytes is called once per picture not held, with done to call when
	// its bytes arrive — from this call or from any later moment. The
	// cache does not wait: the frame that wanted the picture draws its
	// placeholder and the next frame after the bytes arrive draws it, so
	// nothing blocks on a source that is not the local disk. Calling done
	// twice for one item is a mistake and not something to guard.
	//
	// Want, when not nil, is told an item the cache is about to need and
	// has no bytes for, so a view can ask for it before anything draws
	// it: what a Prefetch pass wants must be fetched first and decoded
	// after, and this is how a fetch gets ahead of the scrolling rather
	// than arriving with the frame that shows it. Where Want is nil the
	// bytes are asked for when the picture is decoded, which drops that
	// frame for however long the source takes.
	//
	// Where both are set, PictureAsync is what is used and Picture is the
	// synchronous source a caller that has one keeps. A view with neither
	// decodes nothing.
	PictureAsync *ImageCacheAsync

	// asked is which items have been handed to PictureAsync.Bytes and have
	// not called done yet, so that a frame asking for one already on its
	// way does not ask again, and fetched is the bytes that have arrived.
	// Both are written from the fetch's goroutine and read from the
	// frame's, so both are under mu; nothing else here is shared.
	mu      sync.Mutex
	asked   map[int]bool
	fetched map[int][]byte
	failed  map[int]bool

	// DecodeW and DecodeH are the size a picture is decoded at, in device
	// pixels: the place a cell shows it in. Set them once, from the view
	// (SetDecodeSize) — the cache cannot know the size a view draws at,
	// and both the decode and the room Prefetch leaves are against it.
	DecodeW, DecodeH int

	// Room is what one picture of that size is worth in bytes, which
	// Prefetch checks the cache has room for before it prepares anything.
	// It is one decode's worth of what is held, and is worked out from
	// DecodeW and DecodeH where they are set (SetDecodeSize) or from what
	// is held where they are not.
	Room int

	// items maps an item to what is held for it and bytes is what they hold
	// between them, so the budget is a comparison and not a pass over the
	// map on every picture.
	items map[int]*imageItem
	bytes int

	// tick numbers the frame being built, which each picture is stamped
	// with, and spent is what that frame has spent decoding; deferred
	// counts what its budget left to the next frame.
	tick     uint64
	spent    time.Duration
	deferred int
	// decoded is how many pictures the frame being built has decoded: what
	// a view logs and what tells "this frame decoded nothing" from "this
	// frame decoded", without reading the cache's total twice.
	decoded int

	// prefetching is true while Prefetch decodes, so what it decodes is
	// counted and stamped apart from what a frame draws.
	prefetching bool
	prefetched  int
}

// imageItem is one held picture: what it is, what it cost, and the frame that
// last drew it.
type imageItem struct {
	bitmap *Bitmap
	bytes  int
	used   uint64
}

// SetDecodeSize records the size pictures are decoded at, in device pixels,
// and what one of them is worth: a picture is four bytes a pixel however it
// is drawn, so the room Prefetch leaves is that. Call it from the view, where
// the size of a cell is known — it is not the cache's to work out.
func (c *ImageCache) SetDecodeSize(w, h int) {
	c.DecodeW, c.DecodeH = w, h
	c.Room = w * h * 4
}

// TrackCell asks a grid for the size of its cells each frame and decodes at
// that size, so that a view does not have to remember to: the size a picture
// is decoded at is the size it shows at, and the size it shows at is the
// grid's to measure (CellSize) — not something a view can work out before the
// first frame, when nothing has been measured at all.
//
//	app.pictures.TrackCell(&app.grid)
//
// The width a cell has is not the minimum the grid was asked for: the columns
// share the width the rows have, so a cell is wider by the last column's
// share, and a view that decodes at the minimum decodes every picture a
// halving too large.
func (c *ImageCache) TrackCell(g interface {
	CellSize() (float32, float32, bool)
}, dpr float32) {
	w, h, ok := g.CellSize()
	if !ok {
		return // nothing measured yet: the first frame's estimate stands
	}
	// A picture in a cell shows in Cover, filling the cell's height and
	// overflowing its width where the cell is not as wide as the picture
	// is; a quarter more again covers both, so the halving lands on the
	// cell and not under it.
	pw, ph := int(w*dpr*1.25+0.5), int(h*dpr*1.25+0.5)
	if pw != c.DecodeW || ph != c.DecodeH {
		c.SetDecodeSize(pw, ph)
	}
}

// BeginFrame starts a frame. Every picture the frame draws is stamped with
// it, and the frame's budget and its count of what it deferred are reset.
func (c *ImageCache) BeginFrame() uint64 {
	c.tick++
	c.spent, c.deferred, c.decoded = 0, 0, 0
	return c.tick
}

// Tick is the frame in progress, which BeginFrame numbers. It is for the
// view's own logging and nothing else: the cache is the only thing that
// decides with it.
func (c *ImageCache) Tick() uint64 { return c.tick }

// Deferred reports how many pictures the frame being built left for the next
// one: the ones its budget did not reach, and the ones whose bytes have not
// arrived. A view reads it after building its cells and asks for another
// frame where it is not zero (Context.Invalidate): a budget that spreads
// decoding over frames only arrives if the frames come, and a picture whose
// bytes are still on their way is drawn by a frame that has to be asked for
// too.
func (c *ImageCache) Deferred() int { return c.deferred }

// Decoded reports how many pictures the frame being built has decoded, and
// how long those decodes took. It is what a view logs, and it is why Held is
// not the answer: a view that took Held before and after a cell would be
// reading a cache's total to learn one cell's news, and would get it wrong
// the moment the cache dropped something in between.
func (c *ImageCache) Decoded() (count int, spent time.Duration) { return c.decoded, c.spent }

// Held reports how many pictures the cache holds and what they are worth,
// which is what a meter shows and what a test asserts.
func (c *ImageCache) Held() (count, bytes int) {
	for _, it := range c.items {
		if it.bitmap != nil {
			count++
			bytes += it.bytes
		}
	}
	return count, bytes
}

// Prefetched reports how many pictures Prefetch has decoded ahead of the
// scrolling since it started.
func (c *ImageCache) Prefetched() int { return c.prefetched }

// budget is Budget or its default.
func (c *ImageCache) budget() int {
	if c.Budget > 0 {
		return c.Budget
	}
	return defaultImageBudget
}

const (
	// defaultImageBudget is twice the screen a laptop shows at the cell
	// size the wasm lab's gallery uses, so that scrolling a screenful
	// back and forth decodes nothing twice while the heap stays in the
	// tens of megabytes however big the library is — as long as the view
	// builds only the cells in sight: see Prune, which cannot drop a
	// picture the frame drew.
	defaultImageBudget = 16 << 20
	// defaultFrameBudget is under a 60 Hz refresh and above no picture,
	// so the frame decodes one picture and defers the rest.
	defaultFrameBudget = 3 * time.Millisecond
	// defaultPast is how long a page waits for a quiet moment.
	defaultPast = 180 * time.Millisecond
	// defaultPrefetchRows is how many rows past the screen are prepared.
	defaultPrefetchRows = 2
	// defaultPrefetchBudget is how long one prefetch pass may take.
	defaultPrefetchBudget = 8 * time.Millisecond
	// defaultPoll is how often Run looks for work to do ahead.
	defaultPoll = 100 * time.Millisecond
)

// Bitmap returns the picture held for item, decoding it if this is the first
// frame to draw it or if the cache let it go, and nil when the frame has
// spent its FrameBudget or when Picture returned an error — a cell that
// draws what it is given draws whatever it draws for nil, and the next frame
// asks again.
//
// The picture is decoded at DecodeW by DecodeH, the place a cell shows it in
// rather than its own size (DecodeBitmapSize), which is what makes a library
// of full-size photographs affordable: a 1920-wide capture in a 240-wide
// cell is a quarter of the pixels.
//
// A picture the frame draws is stamped with the frame, so Prune will not
// drop it; one Prefetch decodes is stamped a frame older, since it is off
// screen, so the two competing for the budget puts the one nobody is
// looking at out first.
func (c *ImageCache) Bitmap(item int) *Bitmap {
	if c.items == nil {
		c.items = map[int]*imageItem{}
	}
	it := c.items[item]
	if it != nil && it.bitmap != nil {
		it.used = c.tick
		return it.bitmap
	}
	if b := c.frameBudget(); b > 0 && c.spent >= b {
		if !c.prefetching {
			c.deferred++
		}
		return nil
	}
	if c.Picture == nil && c.PictureAsync == nil {
		return nil
	}
	data, err := c.encoded(item)
	if err != nil {
		return nil
	}
	if data == nil {
		// The bytes are not here: this frame draws whatever a cell draws
		// for no picture, the source was asked for them, and the frame
		// after they arrive draws the picture. Asking again each frame
		// until then would ask the network sixty times a second, so only
		// the first asks.
		//
		// It counts as deferred, because it is left to a later frame as
		// surely as a picture the budget did not reach: a view asks for
		// another frame while Deferred is not zero, and without this the
		// frame that asked is the last one — the bytes arrive and nothing
		// draws them. A source that answers with an error counts as not
		// deferred: nothing is coming, so nothing should ask again.
		if !c.prefetching {
			c.deferred++
		}
		return nil
	}
	start := time.Now()
	bm, err := DecodeBitmapSize(data, c.DecodeW, c.DecodeH)
	c.spent += time.Since(start)
	if err != nil {
		return nil
	}
	if it == nil {
		it = &imageItem{}
		c.items[item] = it
	}
	w, h := bm.Size()
	it.bitmap, it.bytes = bm, w*h*4
	c.bytes += it.bytes
	if c.prefetching {
		c.prefetched++
		it.used = c.tick - min(c.tick, 1)
		return it.bitmap
	}
	c.decoded++
	it.used = c.tick
	return it.bitmap
}

// frameBudget is what one decode may spend: the frame's, or the larger one a
// prefetch pass is allowed.
func (c *ImageCache) frameBudget() time.Duration {
	if c.prefetching {
		if c.PrefetchBudget > 0 {
			return c.PrefetchBudget
		}
		return defaultPrefetchBudget
	}
	if c.FrameBudget > 0 {
		return c.FrameBudget
	}
	if c.FrameBudget == 0 {
		return defaultFrameBudget
	}
	return -1 // set negative: no cap, as a view without a cache
}

// Prune drops whole pictures, least recently drawn first, while the cache
// holds more than Budget. A picture the frame in progress drew is never
// dropped: it is on screen, and dropping it would only decode it again next
// frame.
//
// Picture bounds a frame's work; this bounds the total, and a list that
// scrolls a thousand pictures past and stops holds the sixty of the screen
// rather than the thousand.
//
// That bound is the view's to make reachable, and only a view that builds the
// cells in sight does: a view that builds every cell every frame has drawn
// every picture the cache holds, so nothing here is droppable and the cache
// grows with the library however small Budget is. A grid of a thousand
// pictures built every frame held a thousand of them (measured: 51.8 MB at a
// 16 MB budget). GridView and List build only what shows; a page that lays its
// items out itself has to do the same, or set Budget to what the whole library
// is worth.
func (c *ImageCache) Prune() {
	if c.bytes <= c.budget() {
		return
	}
	keys := make([]int, 0, len(c.items))
	for k, it := range c.items {
		if it.bitmap != nil && it.used != c.tick {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b int) int { return cmp.Compare(c.items[a].used, c.items[b].used) })
	for _, k := range keys {
		if c.bytes <= c.budget() {
			return
		}
		it := c.items[k]
		c.bytes -= it.bytes
		it.bitmap, it.bytes = nil, 0
	}
}

// ImageCacheAsync is the half of a picture source that cannot answer where it
// is asked: where a fetch, a pipe or a database has the bytes and the frame
// cannot wait for them. See ImageCache.PictureAsync.
type ImageCacheAsync struct {
	// Bytes is called once per item whose bytes are not held, with done to
	// call when they arrive. It must not call done before returning from
	// some other goroutine or a later moment: the cache is not waiting.
	Bytes func(item int, done func([]byte, error))
	// Want, when not nil, is told an item the cache is about to need and
	// has no bytes for, so a view can ask for it ahead of the frame that
	// shows it. See ImageCache.Prefetch, which calls it.
	Want func(item int)
}

// encoded returns the item's encoded bytes, or nil where they are not here
// yet and have been asked for. err is for a source that answered and failed.
func (c *ImageCache) encoded(item int) ([]byte, error) {
	if c.PictureAsync == nil {
		return c.Picture(item)
	}
	c.mu.Lock()
	if b, ok := c.fetched[item]; ok {
		c.mu.Unlock()
		return b, nil
	}
	if c.failed[item] {
		// Answered with nothing already: not asked again (see ask), and
		// the view draws its own placeholder for it.
		c.mu.Unlock()
		return nil, errSourceFailed
	}
	already := c.asked[item]
	c.mu.Unlock()
	if !already {
		c.ask(item)
	}
	return nil, nil
}

// errSourceFailed reports a picture whose source answered with nothing, which
// Bitmap turns into the nil a cell draws its placeholder for and Failed
// reports to a view that wants to say more.
var errSourceFailed = errors.New("ui: picture source failed")

// ask hands an item to the source, once: the answer sets fetched and clears
// asked, and until then every frame that wants the picture finds it asked for
// and waits for the answer instead of asking again.
//
// The two maps are under mu because the answer comes on the goroutine the
// fetch runs on — the browser's callback under wasm — and the question comes
// on the frame's, and those are not the same one. Nothing else in the cache
// is shared that way: the frame's own state (tick, spent, the items) is the
// frame goroutine's alone, and a lock there would be paid every picture for
// nothing.
func (c *ImageCache) ask(item int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.asked == nil {
		c.asked = map[int]bool{}
	}
	if c.asked[item] {
		return
	}
	c.asked[item] = true
	src := c.PictureAsync
	src.Bytes(item, func(data []byte, err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.asked, item)
		if err != nil || data == nil {
			// A source that answered and had nothing is remembered as
			// such, and not asked again: a broken URL asked for once a
			// frame is one request per frame for as long as it is on
			// screen, which is the failure a list has to survive without
			// spending anything on it. The item is the view's to retry
			// if it wants (Forget), and nothing else does.
			if c.failed == nil {
				c.failed = map[int]bool{}
			}
			c.failed[item] = true
			return
		}
		if c.fetched == nil {
			c.fetched = map[int][]byte{}
		}
		c.fetched[item] = data
	})
}

// Failed reports whether item's source has answered with nothing, which is
// what a view draws its own placeholder for rather than the one it draws
// while a picture is on its way: the two look the same to a cell and are not
// the same thing to a person — one arrives, the other will not.
func (c *ImageCache) Failed(item int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed[item]
}

// Forget drops what the cache remembers about an item's bytes, so that a
// source which failed is asked for again — the retry a view offers where it
// has one (a button, a timer, coming back into view).
func (c *ImageCache) Forget(item int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failed, item)
	delete(c.fetched, item)
	delete(c.asked, item)
}

// Fetch asks the source for the bytes of every item the frame on screen is
// not showing and Prefetch would prepare next, so that a pass of prefetching
// decodes rather than waits: a prefetch that finds no bytes asks for them and
// does nothing, and the pass after the bytes arrive does the work.
//
// It is what Want is for, and a view calls it where it would call Prefetch —
// in a moment nobody is using the page (Run does it there):
//
//	if n := c.Fetch(); n > 0 { return }  // asked: decode once they arrive
//	c.Prefetch()
//
// It returns how many items it asked for. A source that answers where it is
// asked (Picture, no PictureAsync) has nothing to fetch and this returns 0.
func (c *ImageCache) Fetch() int {
	if c.PictureAsync == nil || c.PictureAsync.Want == nil || c.View == nil {
		return 0
	}
	_, last, cols := c.View()
	if last < 0 {
		return 0
	}
	from, to := last+1, last+1+cols*c.prefetchRows()
	asked := 0
	for i := from; i < to; i++ {
		if c.held(i) || c.buffered(i) || c.isAsked(i) {
			continue
		}
		c.PictureAsync.Want(i)
		c.ask(i)
		asked++
	}
	return asked
}

// buffered reports whether item i's bytes are held but not yet decoded.
func (c *ImageCache) buffered(item int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.fetched[item]
	return ok
}

// isAsked reports whether item i's bytes have been asked for and are on
// their way.
func (c *ImageCache) isAsked(item int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked[item]
}

// Prefetch decodes the pictures of the rows past the ones on screen, within
// PrefetchBudget, and returns how many it decoded.
//
// It is the answer that no frame may own. A picture decoded in the frame
// that draws it costs that frame its whole refresh, so the only way a scroll
// of a screenful drops none is for the pictures to be held before it starts;
// a moment nobody is using the page is where that happens, and Stale is
// whether this is one.
//
// It starts at the row after the last View built, so what it prepares is a
// scroll down: a scroll up has the pictures behind it held already, being
// the ones it came through. It stops when the cache has no room for a row
// (a picture it decodes into a full cache is dropped by the next Prune and
// decoded again, for nothing) and when the pass has spent its budget.
func (c *ImageCache) Prefetch() int {
	if c.PrefetchRows <= 0 || c.View == nil {
		return 0
	}
	_, last, cols := c.View()
	if last < 0 {
		return 0
	}
	room := c.Room
	if room <= 0 {
		room = c.bytes / max(1, len(c.items)) // a picture of what is held
	}
	if c.bytes+cols*room > c.budget() {
		return 0
	}
	from, to := last+1, last+1+cols*c.prefetchRows()
	before := c.prefetched
	c.prefetching = true
	for i := from; i < to; i++ {
		if c.held(i) {
			continue
		}
		if c.Bitmap(i) == nil {
			break // out of budget: the next quiet moment goes on
		}
	}
	c.prefetching = false
	return c.prefetched - before
}

// prefetchRows is PrefetchRows or its default.
func (c *ImageCache) prefetchRows() int {
	if c.PrefetchRows > 0 {
		return c.PrefetchRows
	}
	return defaultPrefetchRows
}

// held reports whether item i is held now, which is what Prefetch skips.
func (c *ImageCache) held(item int) bool {
	it := c.items[item]
	return it != nil && it.bitmap != nil
}

// QuietTimeline is how long the page has gone without input, which the cache reads
// to decide whether a moment is nobody's. Under wasm the framework's
// (mygo.LastInput); off it, whatever the caller has or a constant: a page
// with no input at all is quiet by any measure, so returning a large duration
// is the honest answer there and not a workaround.
//
// A page that has never had an event is the case worth writing down, because
// the zero time is not "just now": the framework's LastInput returns the zero
// Time, and a caller that passes time.Since(zero) as its answer computes a
// duration of five thousand years, which overflows into a negative number.
// QuietSince does that part, so a caller has only to say where the time comes
// from.
type QuietTimeline func() time.Duration

// QuietSince is a Timeline over the last input a framework reports, with the
// zero time read as "never", which is quiet by any measure:
//
//	app.pictures.Run(ui.QuietSince(mygo.LastInput), func(n int) { ... })
//
// It is here and not in the view because every view that runs a cache's
// prefetch has to answer the same two questions the same way: what time is
// it, and what does "never" mean.
func QuietSince(lastInput func() time.Time) QuietTimeline {
	return func() time.Duration {
		if last := lastInput(); !last.IsZero() {
			return time.Since(last)
		}
		return never
	}
}

// never is longer than any run of a program, and the largest a Duration
// holds.
const never = time.Duration(1<<63 - 1)

// Run decodes the rows past the ones on screen whenever the page is quiet,
// on a loop of its own, until stop returns true:
//
//	go c.Run(ui.QuietSince(mygo.LastInput), nil)
//
// It is the third of the cache's three decisions put where it can happen: a
// picture decoded in the frame that draws it drops that frame, so the pictures
// a scroll is about to want must be held before it asks, and the only moment
// with no frame to drop is one nobody is using.
//
// A moment is nobody's when since says so AND the last frame left no cells of
// its own to fill in: those are the cells the person is looking at, and the
// frames filling them have the thread until they are done.
//
// A pass fetches before it decodes (Fetch then Prefetch), so that on a page
// where the bytes are a network away the quiet moment spends itself asking
// rather than waiting, and the pictures are decoded in the quiet moments
// after the answers arrive. Where the source answers in the call the fetch
// does nothing and the pass decodes, which is the whole of it off the page.
//
// The caller drives it — the loop polls, it does not draw, because a backend
// draws the frames the content asks for and no others: a page nobody is
// touching is not drawing frames that decoded nothing, it is not drawing
// frames at all, and there is no frame there to notice the quiet in. Which is
// also why this cannot be started by the cache on its own: whether there is a
// thread to spawn on, and a clock to poll with, is the caller's platform.
//
// past is how often to look, or zero for a tenth of a second. prepare, when
// not nil, is called with how many pictures a pass decoded, which a view logs.
func (c *ImageCache) Run(since QuietTimeline, prepare func(decoded int)) {
	for range time.Tick(c.poll()) {
		if c.PrefetchRows <= 0 || c.View == nil {
			return // nothing to prepare, ever: asked not to, or no grid
		}
		if c.deferred > 0 {
			continue // the frames filling the screen in have the thread
		}
		if !c.Stale(since) {
			continue
		}
		// Ask for the bytes before trying to decode them, on the page: a
		// pass that finds none asks and does nothing, and the pass after
		// they arrive is the one that decodes them. A source that answers
		// where it is asked has nothing to fetch and this is a no-op.
		if c.Fetch() > 0 {
			continue
		}
		if n := c.Prefetch(); n > 0 && prepare != nil {
			prepare(n)
		}
	}
}

// Poll is how often Run looks for work to do ahead, or zero for a tenth of a
// second. A view sets it before Run; nothing else reads it.
func (c *ImageCache) Poll() time.Duration { return c.poll() }

func (c *ImageCache) poll() time.Duration {
	if c.PollEvery > 0 {
		return c.PollEvery
	}
	return defaultPoll
}

// Stale reports whether the page has gone without input long enough for
// Prefetch to run, reading the time since the last input from since: whether
// anyone is using the page is the framework's to know and not the cache's.
//
// It is a time since the last input rather than a run of frames that decoded
// nothing because a backend draws the frames the content asks for and no
// others: a page nobody is touching is not drawing frames that decode
// nothing, it is not drawing frames at all, and there is no frame there to
// notice the quiet in.
func (c *ImageCache) Stale(since QuietTimeline) bool {
	if since == nil {
		return false
	}
	return since() >= c.past()
}

// past is Past or its default.
func (c *ImageCache) past() time.Duration {
	if c.Past > 0 {
		return c.Past
	}
	return defaultPast
}
