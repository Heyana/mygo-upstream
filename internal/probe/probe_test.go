package probe

import (
	"testing"
	"time"

	"github.com/egoist/mygo/internal/scene"
)

// 这一组守探针的**嵌套规则**：内层 span 的时间要从外层扣掉，所以每个名字
// 报的是**它自己的活**。
//
// 为什么必须有：探针是扁平的（map[name]→时间），而它量的东西常是递归的
// （布局的 intrinsicWidths、光栅器的形状遍历）。扁平名字空间遇到嵌套树，
// 必然犯两类错 —— **重复计数**（父子 span 重叠，父项比子项还小，物理上
// 不可能）和 **归属不清**（无名的中间层）。2026-10-14 实测：真机上
// `intrinsic.compute` 1.445ms 而包含它的 `layout` 相位只有 1.008ms。
//
// 一个不可能的数比没有数更坏：人会照着它去做优化。

// spanTimes 取一个名字的自身时间与子时间（纳秒）。
func spanTimes(name string) (self, children int64) {
	for _, c := range Counts() {
		if c.Name == name {
			return c.Self, c.ChildMax
		}
	}
	return 0, 0
}

// TestNestedSpanIsTakenOffItsParent 守最核心的一条：父 span 的自身时间
// 不含子 span。
//
// 变异验证：把 pop 里的 `self := full - childN[d]` 改回 `self := full`，
// 这条立刻红。
func TestNestedSpanIsTakenOffItsParent(t *testing.T) {
	On("all")
	defer Off()
	Reset()

	// 父 span 里套一个睡得久的子 span：父的自身时间必须远小于它的总跨度。
	func() {
		defer Span("outer")()
		func() {
			defer Span("inner")()
			time.Sleep(20 * time.Millisecond)
		}()
		time.Sleep(2 * time.Millisecond)
	}()

	outerSelf, outerChildren := spanTimes("outer")
	innerSelf, _ := spanTimes("inner")

	if innerSelf < int64(15*time.Millisecond) {
		t.Fatalf("inner self %v: the nested span was not timed", time.Duration(innerSelf))
	}
	// 父的自身时间应当只剩那 2ms 左右（留宽裕的余量给调度抖动）。
	if outerSelf > int64(10*time.Millisecond) {
		t.Errorf("outer self is %v beside a child of %v: the child was not taken off its parent",
			time.Duration(outerSelf), time.Duration(innerSelf))
	}
	// 而子的时间要有账可查，不是被丢掉。
	if outerChildren < innerSelf {
		t.Errorf("outer child max %v < inner %v: the child time vanished instead of moving",
			time.Duration(outerChildren), time.Duration(innerSelf))
	}
}

// TestNestedTotalsDoNotExceedTheParent 守"不可能的数"不再出现：
// 父的自身 + 子 ≤ 父的总跨度。这正是真机上 layout 比 intrinsic.compute
// 小的那个 bug。
func TestNestedTotalsDoNotExceedTheParent(t *testing.T) {
	On("all")
	defer Off()
	Reset()

	start := time.Now()
	func() {
		defer Span("phase")()
		for i := 0; i < 3; i++ {
			func() {
				defer Span("step")()
				time.Sleep(3 * time.Millisecond)
			}()
		}
	}()
	total := time.Since(start)

	phaseSelf, phaseChildren := spanTimes("phase")
	if sum := phaseSelf + phaseChildren; time.Duration(sum) > total {
		t.Errorf("phase self %v + children %v = %v, more than the %v it spanned",
			time.Duration(phaseSelf), time.Duration(phaseChildren), time.Duration(sum), total)
	}
	stepSelf, _ := spanTimes("step")
	if time.Duration(stepSelf) > phraseAllowance(total) {
		t.Errorf("three nested steps total %v, more than the phase %v they ran in",
			time.Duration(stepSelf), total)
	}
}

// phraseAllowance 给"3 步合计不能超过父跨度"留 20% 的调度余量。
func phraseAllowance(total time.Duration) time.Duration { return total + total/5 }

// TestSiblingSpansDoNotCancel 守一件事：扣的是**父**，不是兄弟。
// 同级两个 span 各报各的时间，谁也不吃谁。
func TestSiblingSpansDoNotCancel(t *testing.T) {
	On("all")
	defer Off()
	Reset()

	for i := 0; i < 2; i++ {
		func() {
			defer Span("sibling")()
			time.Sleep(5 * time.Millisecond)
		}()
	}
	self, _ := spanTimes("sibling")
	if self < int64(8*time.Millisecond) {
		t.Errorf("two siblings totalled %v: one cancelled the other", time.Duration(self))
	}
}

// TestAtIsNotTakenOffAnyParent 守 At（调用方自己计时的 span）没在栈上，
// 所以它是自己的时间，不该被当成谁的子项扣掉。
//
// 变异验证：把 At 改成也走 push/pop，这条红。
func TestAtIsNotTakenOffAnyParent(t *testing.T) {
	On("all")
	defer Off()
	Reset()

	func() {
		defer Span("parent")()
		At("measured", 4*time.Millisecond)
		time.Sleep(1 * time.Millisecond)
	}()
	self, _ := spanTimes("measured")
	if self < int64(3*time.Millisecond) {
		t.Errorf("At reported %v for a 4ms measurement: it was treated as a nested span",
			time.Duration(self))
	}
}

// TestOffRecordsNothing 守默认关：没开的时候一个名字都不该有。
func TestOffRecordsNothing(t *testing.T) {
	Off()
	func() {
		defer Span("ghost")()
	}()
	if _, children := spanTimes("ghost"); children != 0 || len(Counts()) != 0 {
		t.Errorf("with the probe off, %d names were counted", len(Counts()))
	}
}

// TestOpNameCoversEveryKind 守 op 名字的表没有漏。
//
// 三个后端（CPU 光栅器 / GPU / 浏览器 canvas）都从这一份取名字，所以这里
// 漏一类，三个后端会**一起**漏 —— 而用户读到的就是"这类 op 不存在"。
// scene 加了新的 Kind 而这里没跟，测试必须红。
func TestOpNameCoversEveryKind(t *testing.T) {
	names := map[string]scene.Kind{}
	for k := scene.OpFill; k <= scene.OpHole; k++ {
		n := OpName(k)
		if n == "op/other" {
			t.Errorf("kind %d has no name: it would be reported as op/other", k)
		}
		if prev, dup := names[n]; dup {
			t.Errorf("kinds %d and %d share the name %q", prev, k, n)
		}
		names[n] = k
	}
}

// TestOpNamesMatchTheRasterizers 守逐字对齐：这几个名字是 internal/raster
// （web-wasm 分支）里用的，改任何一个都会让两个后端的读数分裂成两套。
func TestOpNamesMatchTheRasterizers(t *testing.T) {
	want := map[scene.Kind]string{
		scene.OpFill:     "op/fill",
		scene.OpShadow:   "op/shadow",
		scene.OpGlyphs:   "op/glyphs",
		scene.OpImage:    "op/image",
		scene.OpPushClip: "op/pushclip",
		scene.OpPopClip:  "op/popclip",
		scene.OpEffect:   "op/effect",
		scene.OpHole:     "op/hole",
	}
	for k, w := range want {
		if got := OpName(k); got != w {
			t.Errorf("OpName(%d) = %q, want %q", k, got, w)
		}
	}
}

// TestRecursiveChildMaxDoesNotExceedTheFrame 守递归名字的 children 不跨层重复。
//
// 真机实证（2026-10-14，Android 冷启动帧）：`intrinsic.compute` 报出
// children=260.8ms，而**整帧只有 104ms** —— 因为它是递归的（自己调自己 49 次），
// 每次内层的 children 都被累加进同一个名字，同一段时间算了 N 遍。
//
// 判据：children 取**单次最大值**，所以它不可能超过该名字的 Max（单次最长
// 调用），更不可能超过整帧。变异验证：把 ChildMax 改回累计，这条红。
func TestRecursiveChildMaxDoesNotExceedTheFrame(t *testing.T) {
	On("all")
	defer Off()
	Reset()

	// 递归的 span：深度 5，每层各睡一会，内层比外层短。
	var recurse func(depth int)
	recurse = func(depth int) {
		defer Span("recurse")()
		if depth == 0 {
			time.Sleep(10 * time.Millisecond)
			return
		}
		time.Sleep(2 * time.Millisecond)
		recurse(depth - 1)
	}
	start := time.Now()
	recurse(5)
	frame := time.Since(start)

	var calls, self, childMax, max int64
	for _, c := range Counts() {
		if c.Name == "recurse" {
			calls, self, childMax, max = c.Calls, c.Self, c.ChildMax, c.Max
		}
	}
	if calls == 0 {
		t.Fatal("the recursive span recorded nothing")
	}
	if time.Duration(childMax) > frame {
		t.Errorf("child_max %v exceeds the whole frame %v: a recursive name is summing one stretch of time once per level",
			time.Duration(childMax), frame)
	}
	// 它也不能超过单次最长调用 —— 这是同一件事的更强判据。
	if childMax > max {
		t.Errorf("child_max %v > max %v: children were summed across calls rather than taken from the longest",
			time.Duration(childMax), time.Duration(max))
	}
	if self <= 0 {
		t.Error("self recorded nothing: the subtraction left the name empty")
	}
}
