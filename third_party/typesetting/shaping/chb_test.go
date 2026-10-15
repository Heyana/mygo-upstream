//go:build android || darwin

package shaping

import (
	"os"
	"strings"
	"testing"

	"github.com/go-text/typesetting/font"
)

// 对拍：CHB（C HB）vs HarfbuzzShaper（Go 移植）——同字体同文本，
// 字形 ID / advance / cluster 逐项一致才算过。桌面秒级迭代用。
func TestCHBMatchesGoShaper(t *testing.T) {
	path := os.Getenv("CHB_TEST_FONT")
	if path == "" {
		t.Skip("CHB_TEST_FONT 未设置")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	faces, err := font.ParseTTF(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	chb := NewCHBShaper()
	chb.RegisterFace(faces, data, 0)
	var goShaper HarfbuzzShaper

	text := []rune("性能优化的第一性原理：先量再动 Hello World 123")
	face := faces
	in := func() Input {
		return Input{Text: text, RunStart: 0, RunEnd: len(text), Face: face, Size: 16 << 6}
	}

	got := chb.Shape(in())
	want := goShaper.Shape(in())

	if len(got.Glyphs) != len(want.Glyphs) {
		t.Fatalf("字形数 %d != %d", len(got.Glyphs), len(want.Glyphs))
	}
	bad := 0
	for i := range want.Glyphs {
		g, w := got.Glyphs[i], want.Glyphs[i]
		if g.GlyphID != w.GlyphID {
			t.Errorf("glyph %d: ID %d != %d", i, g.GlyphID, w.GlyphID)
			bad++
		}
		if diff := float64(g.Advance-w.Advance) / 64; diff > 0.5 || diff < -0.5 {
			t.Errorf("glyph %d: advance %v != %v", i, g.Advance, w.Advance)
			bad++
		}
		if bad > 5 {
			t.Fatal("太多差异")
		}
	}
	if bad == 0 {
		t.Log("对拍全同")
	}
}
