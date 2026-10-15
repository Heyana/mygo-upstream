//go:build android

// CHBShaper — shaping.Input → C HarfBuzz（libharfbuzz.a）的 Shaper 实现。
//
// 🔴 为什么换掉 HarfbuzzShaper（纯 Go 的 HB 移植，2026-10-15）：Go 移植
// 的 per-Face Font 解析并保留表结构（轮廓点/变体 delta），被布局缓存
// 引用后成为 ~10-15MB 的常驻。C HB 的表访问是 mmap 上的按需解读、
// 零保留——与 glyf/gvar 懒化同一哲学的终局：解析结果不存在。
// 本文件在 vendored 包内，包私有助手（countClusters 等）直接复用。
package shaping

/*
#cgo android CFLAGS: -I${SRCDIR}/../../hb-build/src/lib/android/include/harfbuzz -I${SRCDIR}/../../hb-build/src/lib/android/include/freetype2
#cgo android LDFLAGS: -L${SRCDIR}/../../hb-build/src/lib/android/lib -lharfbuzz -lfreetype -lm -llog
#cgo darwin CFLAGS: -I${SRCDIR}/../../hb-build/src/lib/macos/include/harfbuzz -I${SRCDIR}/../../hb-build/src/lib/macos/include/freetype2
#cgo darwin LDFLAGS: -L${SRCDIR}/../../hb-build/src/lib/macos/lib -lharfbuzz -lfreetype -lm
#include <hb.h>
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"

	"github.com/go-text/typesetting/font"
	"golang.org/x/image/math/fixed"
)

// CHBShaper 用 C HarfBuzz 实现 Shaper。见文件头注释。
type CHBShaper struct {
	// fonts 按 Face 的底层 font.Font 缓存 C 侧 hb_font（含其 face/blob）。
	// blob 引用的字节由 RegisterFace 的调用方持有（Go 侧或 mmap）。
	fonts map[*font.Font]*hbFont
}

type hbFont struct {
	face *C.hb_face_t
	font *C.hb_font_t
	// src 钉住 blob 引用的字体字节：HB 以 READONLY 指针直读它们，
	// 字节不可达就会被 GC 收走 → C 侧悬垂 → hb_shape abort。
	// mmap 来的字节指向映射（映射永不解除），堆字节靠这个引用钉住。
	src []byte
}

// NewCHBShaper 建一个零缓存的 C HB shaper。
func NewCHBShaper() *CHBShaper { return &CHBShaper{fonts: map[*font.Font]*hbFont{}} }

// RegisterFace 把一个 face 的字体源交给 C HB：src 是字体文件（或内存
// 字体）的完整字节，index 是集合内的 face 序号。blob 以 READONLY 引用
// 字节，不拷贝；字节的生命周期由调用方持有（mmap 映射或 app 数据）。
// 同一 font.Font 只注册一次，重复调用是 no-op。
func (t *CHBShaper) RegisterFace(f *font.Face, src []byte, index int) {
	if f == nil || f.Font == nil {
		return
	}
	if _, ok := t.fonts[f.Font]; ok {
		return
	}
	var blob *C.hb_blob_t
	if len(src) > 0 {
		blob = C.hb_blob_create((*C.char)(unsafe.Pointer(&src[0])), C.uint(len(src)),
			C.HB_MEMORY_MODE_READONLY, nil, nil)
	} else {
		return
	}
	cface := C.hb_face_create(blob, C.uint(index))
	C.hb_blob_destroy(blob) // face 持有自己的引用，所有权在此转移
	// 🔴 空脸兜底：WOFF 容器 HB 不认（无 brotli/zlib），glyph_count = 0。
	// 这样的 face 不注册——Shape 回退 Go 移植（typesetting 有 WOFF 解码）；
	// 注册了空脸则全部字形 = 0（真机踩过：整页文字消失成小点）。
	if C.hb_face_get_glyph_count(cface) == 0 {
		C.hb_face_destroy(cface)
		return
	}
	cfont := C.hb_font_create(cface)
	upem := int(C.hb_face_get_upem(cface))
	C.hb_font_set_scale(cfont, C.int(upem), C.int(upem))
	t.fonts[f.Font] = &hbFont{face: cface, font: cfont, src: src}
}

// Shape 用 C HarfBuzz 整形一个 run，产出与 HarfbuzzShaper.Shape 相同
// 结构的 Output（cluster/方向/尺寸的语义逐项对齐，见包内 countClusters）。
func (t *CHBShaper) Shape(input Input) Output {
	runes, start, end := input.Text, input.RunStart, input.RunEnd
	if end < start {
		end, start = start, end
	}
	start = clamp(start, 0, len(runes))
	end = clamp(end, 0, len(runes))

	// 🔴 BISECT-1 结论：RegisterFace 无罪。BISECT-2：跳过 extents。
	hf, ok := t.fonts[input.Face.Font]
	if !ok {
		var fallback HarfbuzzShaper
		return fallback.Shape(input)
	}

	buf := C.hb_buffer_create()
	defer C.hb_buffer_destroy(buf)
	C.hb_buffer_pre_allocate(buf, C.uint(end-start))

	// runes → UTF-32。🔴 传全量文本 + item_offset/item_length：hb 给
	// cluster 赋的是"数组内索引"，传全量才能得到与 Go 移植一致的
	// 绝对 rune 索引（countClusters/换行/下游对拍全按绝对索引）。
	u32 := make([]C.uint32_t, len(runes))
	for i, r := range runes {
		u32[i] = C.uint32_t(r)
	}
	if len(u32) > 0 && end > start {
		C.hb_buffer_add_utf32(buf, &u32[0], C.int(len(u32)), C.uint(start), C.int(end-start))
	}

	// 方向：Go 移植的 Direction 枚举值与 C HB 相同（4+iota）。
	hbdir := input.Direction.Harfbuzz()
	C.hb_buffer_set_direction(buf, C.hb_direction_t(uint32(hbdir)))
	// BISECT-3：script/language 暂停（cstr("") 的 StringData 未定义行为嫌疑）
	if len(input.Language) > 0 {
		C.hb_buffer_set_language(buf, C.hb_language_from_string(cstr(string(input.Language)), C.int(len(input.Language))))
	}
	if s := input.Script.String(); len(s) == 4 {
		C.hb_buffer_set_script(buf, C.hb_script_from_string(cstr(s), 4))
	}

	// scale：input.Size 本身就是 26.6（px*64）；HB 的 pos = units ×
	// scale/upem，设 scale = Size → pos 直接是 26.6（与 Go 移植的
	// XScale=size<<6 再 >>6 等价）。🔴 不能用 Ceil()（那是像素数），
	// 会把整页文字缩小 64 倍成小点。
	size26 := int32(input.Size)
	C.hb_font_set_scale(hf.font, C.int(size26), C.int(size26))

	// features：FontFeature 只有 Tag+Value（全局生效）
	var cfeats []C.hb_feature_t
	for _, f := range input.FontFeatures {
		cfeats = append(cfeats, C.hb_feature_t{
			tag:   C.hb_tag_t(f.Tag),
			value: C.uint(f.Value),
			start: 0,
			end:   C.uint(0xFFFFFFFF),
		})
	}
	var featsPtr *C.hb_feature_t
	if len(cfeats) > 0 {
		featsPtr = &cfeats[0]
	}
	C.hb_shape(hf.font, buf, featsPtr, C.uint(len(cfeats)))

	var nInf, nPos C.uint
	infos := C.hb_buffer_get_glyph_infos(buf, &nInf)
	poss := C.hb_buffer_get_glyph_positions(buf, &nPos)
	gi := unsafe.Slice((*C.hb_glyph_info_t)(unsafe.Pointer(infos)), int(nInf))
	gp := unsafe.Slice((*C.hb_glyph_position_t)(unsafe.Pointer(poss)), int(nPos))

	isVertical := input.Direction.IsVertical()
	glyphs := make([]Glyph, len(gi))
	for i := range glyphs {
		glyphs[i] = Glyph{
			ClusterIndex: int(gi[i].cluster),
			GlyphID:      gID(gi[i].codepoint),
			Mask:         uint32(gi[i].mask),
		}
		if isVertical {
			glyphs[i].YAdvance = fixed26(int(gp[i].y_advance))
			glyphs[i].Advance = glyphs[i].YAdvance
		} else {
			glyphs[i].XAdvance = fixed26(int(gp[i].x_advance))
			glyphs[i].Advance = glyphs[i].XAdvance
		}
		glyphs[i].XOffset = fixed26(int(gp[i].x_offset))
		glyphs[i].YOffset = fixed26(int(gp[i].y_offset))

		// 轮廓度量：C HB 按需取（mmap 现读），不驻留。
		var ext C.hb_glyph_extents_t
		if C.hb_font_get_glyph_extents(hf.font, C.hb_codepoint_t(gi[i].codepoint), &ext) != 0 {
			glyphs[i].Width = fixed26(int(ext.width))
			glyphs[i].Height = fixed26(int(ext.height))
			glyphs[i].XBearing = fixed26(int(ext.x_bearing))
			glyphs[i].YBearing = fixed26(int(ext.y_bearing))
		}
	}
	countClusters(glyphs, input.RunEnd, input.Direction.Progression())
	out := Output{
		Glyphs:    glyphs,
		Direction: input.Direction,
		Face:      input.Face,
		Size:      input.Size,
	}
	out.Runes.Offset = input.RunStart
	out.Runes.Count = input.RunEnd - input.RunStart

	if input.Direction.IsSideways() {
		out.sideways()
	}

	// 行界：C 侧按方向取字体级 extents。
	var fext C.hb_font_extents_t
	hdir := C.hb_direction_t(uint32(hbdir))
	C.hb_font_get_extents_for_direction(hf.font, hdir, &fext)
	out.LineBounds = Bounds{
		Ascent:  fixed26(int(fext.ascender)),
		Descent: fixed26(int(fext.descender)),
		Gap:     fixed26(int(fext.line_gap)),
	}
	out.RecalculateAll()
	replaceNotSupportedSpaces(input.Text, out.Glyphs)
	return out
}

// gID 把 C 的 codepoint（整形后即字形 ID）转成包内类型。
func gID(cp C.hb_codepoint_t) font.GID { return font.GID(cp) }

func cstr(s string) *C.char { return (*C.char)(unsafe.Pointer(unsafe.StringData(s))) }

// fixed26 把整数转成包内用的 fixed.Int26_6（typesetting 的 go.mod 锁
// go1.19，unsafe.StringData 需要 1.20，所以 fixed 引用走 mygo 侧同源
// 包——直接用 golang.org/x/image/math/fixed 的 Int26_6 构造）。
func fixed26(v int) fixed.Int26_6 { return fixed.Int26_6(v) }
