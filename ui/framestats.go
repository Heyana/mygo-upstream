package ui

import (
	"fmt"
	"log"
	"os"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"
)

// Frame statistics: with MYGO_FRAME_STATS set, the engine logs each frame
// slower than a threshold, with how long each part took, which path drew
// it, what the process allocated meanwhile and whether the garbage
// collector ran. Unset, frames measure nothing.

// frameStatsThreshold is how long a frame takes before it is logged, from
// MYGO_FRAME_STATS: 1 (or true, on) logs those over 8 ms, another number
// those over that many milliseconds, and all every frame. frameStatsOn
// tells whether it is set.
var frameStatsThreshold, frameStatsOn = frameStatsSetting(os.Getenv("MYGO_FRAME_STATS"))

// SetFrameStats turns the per-frame log on or off at run time, and sets the
// threshold in milliseconds below which a frame is not logged (0 logs every
// frame). An app on Android has no environment of its own — the shell's does
// not reach it — so the switch the environment reads at start-up is useless
// there, and this is how an Android build turns measurement on.
//
// It is safe to call before the first frame and does nothing after that:
// the stats are read on the first frame's begin, so the switch has to be
// set before the app runs.
func SetFrameStats(threshold time.Duration) {
	frameStatsThreshold, frameStatsOn = threshold, true
}

// frameLog writes the log of a frame; tests replace it.
var frameLog = log.Print

func frameStatsSetting(v string) (threshold time.Duration, on bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "off", "no":
		return 0, false
	case "1", "true", "on", "yes":
		return 8 * time.Millisecond, true
	case "all":
		return 0, true
	}
	ms, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || ms < 0 {
		log.Printf("mygo: MYGO_FRAME_STATS=%q is not a number of milliseconds: logging frames over 8 ms", v)
		return 8 * time.Millisecond, true
	}
	return time.Duration(ms * float64(time.Millisecond)), true
}

// The parts of a frame.
const (
	phaseBuild = iota
	phaseLayout
	phasePaint
	phasePresent
	phases
)

// The runtime's counters a frame reads before and after.
const (
	statObjects = iota // allocations, but tiny ones
	statTiny           // tiny allocations, combined in blocks
	statBytes
	statCycles // GC cycles ended
	statAssist // CPU seconds goroutines helped the GC mark
	stats
)

// frameStats measures a frame for MYGO_FRAME_STATS.
type frameStats struct {
	threshold   time.Duration
	start, last time.Time
	laps        [phases]time.Duration
	passes      int
	samples     [stats]metrics.Sample
	before      [stats]float64
	layouts     uint64 // the text system's, before the frame
}

func newFrameStats(threshold time.Duration) *frameStats {
	f := &frameStats{threshold: threshold}
	for i, name := range [stats]string{
		"/gc/heap/allocs:objects",
		"/gc/heap/tiny/allocs:objects",
		"/gc/heap/allocs:bytes",
		"/gc/cycles/total:gc-cycles",
		"/cpu/classes/gc/mark/assist:cpu-seconds",
	} {
		f.samples[i].Name = name
	}
	return f
}

// read returns the runtime's counters.
func (f *frameStats) read() (v [stats]float64) {
	metrics.Read(f.samples[:])
	for i, s := range f.samples {
		switch s.Value.Kind() {
		case metrics.KindUint64:
			v[i] = float64(s.Value.Uint64())
		case metrics.KindFloat64:
			v[i] = s.Value.Float64()
		}
	}
	return v
}

// begin starts measuring a frame. The methods of a nil frameStats do
// nothing, as frames go while MYGO_FRAME_STATS is unset.
func (f *frameStats) begin(rt *engine) {
	if f == nil {
		return
	}
	f.before, f.layouts = f.read(), rt.text.LayoutsMade()
	f.laps, f.passes = [phases]time.Duration{}, 0
	f.start = time.Now()
	f.last = f.start
}

// lap ends a part of the frame.
func (f *frameStats) lap(phase int) {
	if f == nil {
		return
	}
	now := time.Now()
	f.laps[phase] += now.Sub(f.last)
	f.last = now
}

// end logs the frame if it took longer than the threshold.
func (f *frameStats) end(rt *engine) {
	if f == nil {
		return
	}
	total := time.Since(f.start)
	// The last frame's parts, kept for a display to show: a readout that
	// wants to say WHERE a frame's time goes cannot read a log (writing one
	// costs what it measures — a log.Print is several milliseconds), and
	// the frame's own numbers are gone by the time the next one starts.
	lastLaps = f.laps
	lastTotal = total
	if total < f.threshold {
		return
	}
	after := f.read()
	var d [stats]float64
	for i := range d {
		d[i] = after[i] - f.before[i]
	}
	ms := func(t time.Duration) string { return strconv.FormatFloat(t.Seconds()*1000, 'f', 1, 64) }
	other := total
	for _, t := range f.laps {
		other -= t
	}
	path := "not shown"
	if p, ok := rt.host.(interface{ framePath() string }); ok {
		if fp := p.framePath(); fp != "" {
			path = fp
		}
	}
	passes := ""
	switch {
	case f.passes == 0:
		passes = " (painted again)" // engine.repaintFrame
	case f.passes > 1:
		passes = fmt.Sprintf(" (%d passes)", f.passes)
	}
	gc := "no GC"
	if d[statCycles] > 0 || d[statAssist] > 0 {
		gc = fmt.Sprintf("GC: %.0f cycles ended, %s ms of assists", d[statCycles], ms(time.Duration(d[statAssist]*float64(time.Second))))
	}
	frameLog(fmt.Sprintf("mygo: frame %d took %s ms: build %s%s, layout %s, paint %s, present %s (%s), other %s; meanwhile the process made about %.0f allocations, %.1f KB, %d text layouts; %s",
		rt.frame, ms(total), ms(f.laps[phaseBuild]), passes, ms(f.laps[phaseLayout]), ms(f.laps[phasePaint]), ms(f.laps[phasePresent]), path, ms(other),
		d[statObjects]+d[statTiny], d[statBytes]/1024, rt.text.LayoutsMade()-f.layouts, gc))
}

// PhaseReading is the parts of the last frame that was measured, and how
// long it took in all. A display can show them (the lab's HUD does) — the
// frame's own numbers are overwritten by the next frame, and a log is not a
// place to read them from: measuring by printing costs more than what it
// measures.
type PhaseReading struct {
	Build, Layout, Paint, Present, Other time.Duration
	Total                                time.Duration
	Valid                                bool
}

// FramePhases returns the parts of the last measured frame.
func FramePhases() PhaseReading {
	r := PhaseReading{Build: lastLaps[phaseBuild], Layout: lastLaps[phaseLayout],
		Paint: lastLaps[phasePaint], Present: lastLaps[phasePresent], Total: lastTotal}
	r.Other = r.Total - r.Build - r.Layout - r.Paint - r.Present
	r.Valid = r.Total > 0
	return r
}

// lastLaps and lastTotal are the parts of the last frame measured (end),
// written by the pump goroutine and read by a display on it too.
var (
	lastLaps  [phases]time.Duration
	lastTotal time.Duration
)
