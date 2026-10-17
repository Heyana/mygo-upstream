// Package probe is the frame's named stopwatch, shared by every renderer.
//
// Why it exists: a phase's milliseconds answer "which part of the frame is
// big", and that is the wrong question once the part is not big. The 1622 ms
// transition frame showed it — the phases said "raster", the op kinds said
// "fill", and neither said which line. What was needed was "which line, and
// how many times", and that is what this counts.
//
// It is one package rather than one per renderer because a renderer's
// timings are only comparable with another's when the names are the same.
// The CPU rasterizer, the browser's canvas and every GPU backend time the
// same spans under the same names — op/fill, op/glyphs, op/shadow,
// render/clear — so a reading of a frame on a phone and the same frame in a
// browser line up, and a number that looks large in one is large in the
// other.
//
// Names are hierarchical with "/", so a prefix is a switch: Probes("op")
// covers op/fill and op/glyphs. That is what makes it usable while hunting —
// the first pass turns everything on, the second narrows to the suspect
// without editing code.
//
// It is off unless an app turns it on, and off costs nothing: ProbeSpan
// returns an empty func and the render loop checks one boolean. A clock read
// per operation is not free at thousands of operations a frame, and
// measuring with the timer on would change what it measures.
//
//	probe.On("all")            // everything, for the first look
//	probe.On("op/fill")        // one of them
//	probe.Off()                // off
//	probe.Counts()             // what was called, how often, how long, longest first
//	probe.Reset()              // forget, so a reading is one frame's
package probe

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/internal/scene"
)

// stat is one named probe's totals since the last reset.
type stat struct {
	// Calls is how many times the name was entered. Max is the longest single
	// call: a mean hides the one call that is the whole frame, and that call
	// is usually the finding.
	//
	// Self is the span's OWN time: the time inside it that no span nested in
	// it accounted for. A span around a recursive function would otherwise
	// count the same time once per level, and a parent would come out smaller
	// than the children it contains — which is impossible, and which the
	// layout phase did report before this was subtracted.
	//
	// 🔴 It is NOT the span's whole time. Self+ChildMax is. Reading Self as
	// the total makes a parent look smaller than the children inside it, and
	// that mistake was made twice against this readout before the field was
	// renamed from Nanos to say so.
	//
	// ChildMax is the most one call spent inside itself, so a reader can see
	// Nanos+ChildMax as that call's whole span. It is not a sum over calls: a
	// recursive name would then count one stretch of time once per level (see
	// pop). Nested is how many calls had anything inside them.
	Calls    int64
	Self     int64
	Max      int64
	ChildMax int64
	Nested   int64
}

// The state. The renderer reads on without the lock (a bool write is atomic
// on every platform Go runs on, and a frame that says "off" one call late is
// worth not locking every op for); the counters take it, because a frame's
// work may run on several goroutines at once and a plain += on a shared map
// element is a race that loses counts.
var (
	mu    sync.Mutex
	on    bool
	stats map[string]*stat
	// enabled is the set of prefixes that are on. Empty and on false are the
	// same thing: nothing is measured.
	enabled []string

	// depth is how many spans are open right now, and frames[d] is the
	// frame's open spans at that depth, innermost last. A span that opens
	// while another is open is a child of it: its time is taken off the
	// parent's, so that a name's Self is its own work and the totals of
	// nested names cannot add up to more than the phase they live in.
	//
	// It is per goroutine in effect and not by construction: the counters
	// take mu, and a frame's work on several goroutines would interleave
	// their nesting. That is why the parent's time is subtracted with the
	// lock held, and why a span whose parent belongs to another goroutine
	// still reports its own time (it just does not get subtracted from a
	// parent that is not on this stack).
	depth int
	// childN[d] is the child time accumulated at depth d for the span open
	// there, taken off that span's own time when it ends.
	childN [maxDepth]int64
)

// maxDepth is how deep spans may nest before the innermost ones stop being
// charged to their parent. 32 is past any call stack this measures — the
// deepest is the layout's recursion, whose spans are named leaf.textLayout
// and the like and do not nest.
const maxDepth = 32

// On turns probes on by prefix. With "all" it turns every probe on; with any
// other name it turns on the probes whose name is that name or begins with it
// and "/". Calling it again replaces the set, so it is the one call a page
// makes when the person picks a probe to watch.
//
// A name may also carry several prefixes separated by commas, which is what a
// page's query string arrives as: ?probe=op/fill,clip/solid is one string and
// two probes. Splitting it here rather than at the call site keeps every
// caller — a page, a flag, a test — able to say the same thing.
func On(prefixes ...string) {
	mu.Lock()
	defer mu.Unlock()
	stats = map[string]*stat{}
	enabled = nil
	any := false
	for _, arg := range prefixes {
		for _, p := range strings.Split(arg, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			any = true
			if p == "all" {
				// "all" is the empty prefix, which every name begins with:
				// nothing else needs recording once it is in the set.
				enabled = nil
				on = true
				return
			}
			enabled = append(enabled, p)
		}
	}
	on = any
}

// Off turns every probe off and forgets what they held.
func Off() { On() }

// wanted reports whether name is covered by the set On turned on. "all" (no
// enabled prefixes) covers everything.
func wanted(name string) bool {
	if len(enabled) == 0 {
		return true
	}
	for _, p := range enabled {
		if name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

// Active reports whether anything is being measured, so a caller can skip
// building the arguments a probe would need.
func Active() bool { return on }

// Span marks a named span and returns the func to end it:
//
//	defer probe.Span("render/clear")()
//
// It returns a no-op where the name is not what On turned on, so the call site
// is one line either way, and costs one compare when the probe is off.
//
// A span opened inside another is its child: the child's time comes off the
// parent's, so each name reports its own work. Without that, a span around a
// recursive call counts one time per level, and a parent can come out smaller
// than the children it contains — an impossible number a reader would then
// optimize against. See stat.Self.
func Span(name string) func() {
	if !on || !wanted(name) {
		return func() {}
	}
	t := time.Now()
	tok := Begin(name)
	return func() { End(tok, time.Since(t)) }
}

// Begin and End are Span split in two, for a span whose start and end are not
// in one function — the shape a caller that marks a start needs:
//
//	tok := probe.Begin("layout")
//	... work ...
//	probe.End(tok, time.Since(start))
//
// 🔴 They nest with Span, and that is the point: the engine's own calls use
// this pair (ui.ProbeStart/ProbeEnd), and those are the spans around the
// recursive work — intrinsic.compute inside layout, a glyph run inside paint.
// Timing them outside the stack is what let a child report more time than the
// phase it ran in.
// Token is a span Begin opened: where it sits on the stack and the name it
// will be recorded under. The zero token means nothing is being measured.
type Token struct {
	depth int
	name  string
	open  bool
}

// Begin opens a span and returns the token End takes. The name is taken here
// for a caller that knows it up front; a caller that names the span where it
// ends uses BeginUnnamed and EndNamed instead. A name that the filter leaves
// out opens nothing, and its End does nothing.
func Begin(name string) Token {
	if !on || !wanted(name) {
		return Token{}
	}
	return push(name)
}

// BeginUnnamed opens a span whose name is given to EndNamed, so that a caller
// with one marker and the name only at the end needs no second call.
func BeginUnnamed() Token {
	if !on {
		return Token{}
	}
	return push("")
}

// End closes a span opened by Begin, charging what its children took to the
// parent rather than to it.
func End(tok Token, d time.Duration) {
	if !tok.open {
		return
	}
	pop(tok, d)
}

// EndNamed closes a span opened by BeginUnnamed and records it under name.
// A name the filter leaves out ends the span without recording it, which is
// what keeps the stack balanced whatever the filter says.
func EndNamed(tok Token, name string, d time.Duration) {
	if !tok.open {
		return
	}
	tok.name = name
	// The filter is checked here rather than at the start, since the name is
	// only known here. A span the filter leaves out still pops.
	if !wanted(name) {
		mu.Lock()
		if depth > 0 {
			depth--
		}
		mu.Unlock()
		return
	}
	pop(tok, d)
}

// At records a span the caller timed itself and that has no place on the
// stack: it is its own time and is not taken off any parent. For a span that
// nests, use Begin/End.
func At(name string, d time.Duration) {
	if !on || !wanted(name) {
		return
	}
	add(name, d.Nanoseconds())
}

// Bump counts a call without timing it: how many ops a frame drew or how many
// times a primitive ran is often the whole answer, and a counter is free where
// a stopwatch is not.
func Bump(name string) {
	if !on || !wanted(name) {
		return
	}
	mu.Lock()
	s := stats[name]
	if s == nil {
		s = &stat{}
		stats[name] = s
	}
	s.Calls++
	mu.Unlock()
}

// push opens a span at the next depth and returns the token End takes.
func push(name string) Token {
	mu.Lock()
	defer mu.Unlock()
	d := depth
	if d >= maxDepth {
		d = maxDepth - 1
	}
	childN[d] = 0
	depth++
	return Token{depth: d, name: name, open: true}
}

// pop closes a span, charging what its children took to the parent rather
// than to it: a name's Self is then the work nothing inside it accounted for.
func pop(tok Token, own time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	if depth > 0 {
		depth--
	}
	d := tok.depth
	if d >= maxDepth {
		d = maxDepth - 1
	}
	full := own.Nanoseconds()
	// The parent is the span that was open at d-1: what this one took,
	// children and all, is time the parent did not spend in itself.
	if d > 0 {
		childN[d-1] += full
	}
	s := stats[tok.name]
	if s == nil {
		s = &stat{}
		stats[tok.name] = s
	}
	self := full - childN[d]
	if self < 0 {
		// Clocks that do not nest must not write a negative time into the
		// readout.
		self = 0
	}
	s.Calls++
	s.Self += self
	// Children is the most any single call spent inside itself, not the sum
	// over calls: a recursive name (intrinsic.compute calls itself) has each
	// inner call's children already inside the outer call's, so summing them
	// counts one stretch of time once per level — the readout showed
	// intrinsic.compute with 260 ms of children inside a 104 ms frame.
	//
	// The largest single call is the one that answers "how much of this name
	// is somebody else's work", the same way Max answers it for the whole
	// span. A reader who wants the total has Calls.
	if c := childN[d]; c > s.ChildMax {
		s.ChildMax = c
	}
	if childN[d] > 0 {
		s.Nested++
	}
	if full > s.Max {
		s.Max = full
	}
}

func add(name string, n int64) {
	mu.Lock()
	defer mu.Unlock()
	s := stats[name]
	if s == nil {
		s = &stat{}
		stats[name] = s
	}
	s.Calls++
	s.Self += n
	if n > s.Max {
		s.Max = n
	}
}

// Count is one name's totals, as a readout holds them.
type Count struct {
	Name  string `json:"name"`
	Calls int64  `json:"calls"`
	// Self is the span's own time and ChildMax the most one call spent inside
	// itself — Self+ChildMax is the call's whole span. Max is the longest
	// single call as it was timed, children and all. Nested says how many
	// calls had something inside them, which is what tells a leaf from a
	// branch.
	//
	// 🔴 Self is not the total: see stat.Self.
	Self     int64 `json:"self"`
	Max      int64 `json:"max"`
	ChildMax int64 `json:"child_max,omitempty"`
	Nested   int64 `json:"nested,omitempty"`
}

// Counts returns every probe's totals, sorted by total time descending so the
// first line is the thing to look at.
func Counts() []Count {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Count, 0, len(stats))
	for name, s := range stats {
		out = append(out, Count{Name: name, Calls: s.Calls, Self: s.Self, Max: s.Max,
			ChildMax: s.ChildMax, Nested: s.Nested})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Self > out[j].Self })
	return out
}

// Reset forgets every count, so a reading is one frame's rather than the
// process's. Called before the frame to sample.
func Reset() {
	mu.Lock()
	stats = map[string]*stat{}
	mu.Unlock()
}

// OpName is the probe name of a kind of scene op, as every renderer names the
// same span: op/fill is op/fill on the CPU rasterizer, on the GPU and on the
// browser's canvas. One definition is what keeps them from drifting — three
// copies would eventually disagree, and a reading of a frame would then not
// line up between backends, which is the whole point of the shared names.
func OpName(k scene.Kind) string {
	switch k {
	case scene.OpFill:
		return "op/fill"
	case scene.OpShadow:
		return "op/shadow"
	case scene.OpGlyphs:
		return "op/glyphs"
	case scene.OpImage:
		return "op/image"
	case scene.OpPushClip:
		return "op/pushclip"
	case scene.OpPopClip:
		return "op/popclip"
	case scene.OpEffect:
		return "op/effect"
	case scene.OpHole:
		return "op/hole"
	}
	return "op/other"
}
