// Quotes-web is the 40-row quotes scenario of ui/perf_bench_test.go, as a
// real app that runs in a browser (and on a phone, over the LAN).
//
//	go run ./examples/quotes-web
//	go run ./cmd/mygo build ./examples/quotes-web -platform web
//
// It exists to answer one question with a stopwatch instead of an opinion:
// what does a ticking table cost in the browser, on the device that actually
// runs it. The per-frame work is the same as BenchmarkQuotes — a quarter of
// the rows change price every frame, every row is re-rendered from the state.
package main

import (
	"fmt"
	"log"
	"math/rand/v2"
	"syscall/js"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const quoteRows = 40

// symbolFor makes a stable 4-letter ticker for row i.
func symbolFor(i int) string {
	var b [4]byte
	n := i*2654435761 + 1
	for j := range b {
		b[j] = byte('A' + (n>>(j*5))%26)
	}
	return string(b[:])
}

type quotePage struct {
	symbol [quoteRows]string
	price  [quoteRows]float64
	open   [quoteRows]float64
	volume [quoteRows]uint64
	rng    *rand.Rand

	ticks int
}

func newQuotePage() *quotePage {
	q := &quotePage{rng: rand.New(rand.NewPCG(11, 13))}
	for i := range quoteRows {
		q.symbol[i] = symbolFor(i)
		q.price[i] = 20 + q.rng.Float64()*480
		q.open[i] = q.price[i]
		q.volume[i] = uint64(q.rng.IntN(1_000_000))
	}
	return q
}

// tick moves a quarter of the rows, as the benchmark does.
func (q *quotePage) tick() {
	for range quoteRows / 4 {
		i := q.rng.IntN(quoteRows)
		q.price[i] *= 1 + (q.rng.Float64()-0.5)*0.004
		q.volume[i] += uint64(q.rng.IntN(500))
	}
	q.ticks++
}

// noTick 由页面设置：window.__noTick = true 时数据不动，
// 用来分辨「每帧成本」是来自新字形光栅化还是 draw call。
var noTick = func() bool {
	v := js.Global().Get("__noTick")
	return !v.IsUndefined() && v.Bool()
}

func (q *quotePage) view(c *ui.Context) {
	if !noTick() {
		q.tick()
	}
	// Repaint every frame: this is the animated case, not an idle one.
	c.AnimationFrame()
	t := c.Theme()
	ui.Column(c).Fill().Padding(16).Gap(2).Children(func() {
		ui.Row(c).Gap(12).Height(20).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Symbol").Width(80).Bold().TextColor(t.TextMuted)
			ui.Text(c, "Price").Width(90).TextAlign(ui.End).Bold().TextColor(t.TextMuted)
			ui.Text(c, "Change").Width(80).TextAlign(ui.End).Bold().TextColor(t.TextMuted)
			ui.Text(c, "Volume").Width(100).TextAlign(ui.End).Bold().TextColor(t.TextMuted)
		})
		for i := range quoteRows {
			ch := q.price[i] - q.open[i]
			col := t.Success
			if ch < 0 {
				col = t.Danger
			}
			ui.Row(c).Gap(12).Height(22).AlignItems(ui.Center).Children(func() {
				ui.Text(c, q.symbol[i]).Width(80).Bold()
				ui.Textf(c, "%.2f", q.price[i]).Width(90).TextAlign(ui.End)
				ui.Textf(c, "%+.2f", ch).Width(80).TextAlign(ui.End).TextColor(col)
				ui.Text(c, fmt.Sprintf("%d", q.volume[i])).Width(100).TextAlign(ui.End).TextColor(t.TextMuted)
			})
		}
	})
}

func main() {
	q := newQuotePage()
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Quotes",
			Width:   420,
			Height:  900,
			Content: ui.View(q.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
