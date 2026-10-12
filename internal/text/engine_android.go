//go:build android && cgo

package text

/*
#include <stdlib.h>
#include <android/font.h>
#include <android/system_fonts.h>
#include <android/font_matcher.h>

// The font APIs of libandroid (AFont, AFontMatcher and
// ASystemFontIterator) are there since API 29; the CC in a cross build is
// the NDK's aarch64-linux-android29-clang, so the calls below link.
#cgo LDFLAGS: -landroid
*/
import "C"

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"math"
	"os"
	"sort"
	"strings"
	"unsafe"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/egoist/mygo/internal/scene"
)

// newEngine returns the Android engine: the fonts are the system's own and
// the choice of one is the system's own (libandroid's AFontMatcher and
// ASystemFontIterator), while shaping, line breaking and rasterizing are
// go-text's, since Minikin and Skia are not part of the NDK.
func newEngine() engine { return newAndroidEngine() }

// androidEngine lays out and draws text with go-text over the fonts
// Android has.
//
// Its face of a style is what Android's own font matcher picks for the
// style's family, weight and italic; what that font lacks is what the
// matcher picks for the rune itself, which is Android's fallback chain.
// Each font is read from the file the matcher names (AFont_getFontFilePath)
// and parsed once, by path.
//
// The engine is called with the System's lock held and from Go, never from
// a C callback, so it needs no locking of its own and passes no Go pointer
// to C.
type androidEngine struct {
	shapeScratch

	shaper  shaping.HarfbuzzShaper
	segment shaping.Segmenter
	wrap    shaping.LineWrapper

	// fonts are the Fonts handed out, one per face and size.
	fonts map[androidFontKey]*Font
	// faces are the fonts read from font files; Font.native indexes it.
	faces []*androidFont
	// index is the face of each (font file, place in it) once it is read.
	index map[androidFileKey]int
	// fileFaces are the faces of each font file, parsed once, by path.
	fileFaces map[string][]*font.Face
	// faceIndex is the place in faces of each parsed face, for the shaper's
	// Output, which names a face rather than a Font.
	faceIndex map[*font.Face]int
	// styleFaces is the face each style's families resolve to.
	styleFaces map[androidStyleKey]int
	// fallbacks is the face that covers a rune a style's own font lacks.
	fallbacks map[androidFallbackKey]int
	// app are the faces of the fonts the app added, by family.
	app map[string][]int
	// system are the system's fonts, enumerated once; families is the
	// family each font file declares, read once, by path.
	system     []androidSystemFont
	enumerated bool
	families   map[string]string
}

// androidFontKey names a Font: a face at a size.
type androidFontKey struct {
	face int
	size float32
}

// androidFileKey names a face: a font file, and its place in the file (a
// font collection holds several).
type androidFileKey struct {
	path  string
	index int
}

// androidStyleKey names what a face is chosen for.
type androidStyleKey struct {
	family string
	weight int
	italic bool
}

// androidFallbackKey names a fallback: the family that lacked a rune, and
// the rune.
type androidFallbackKey struct {
	family string
	r      rune
}

// androidFont is a font read from a font file.
type androidFont struct {
	face *font.Face
	upem float32
}

// androidSystemFont is a font Android has: the file it lives in, its place
// in the file, and the weight and italic the file's font declares.
type androidSystemFont struct {
	file   string
	index  int
	weight int
	italic bool
}

func newAndroidEngine() *androidEngine {
	return &androidEngine{
		fonts:      map[androidFontKey]*Font{},
		index:      map[androidFileKey]int{},
		fileFaces:  map[string][]*font.Face{},
		faceIndex:  map[*font.Face]int{},
		styleFaces: map[androidStyleKey]int{},
		fallbacks:  map[androidFallbackKey]int{},
		app:        map[string][]int{},
	}
}

// faceAt returns the face at index i, or nil.
func (e *androidEngine) faceAt(i int) *androidFont {
	if i < 0 || i >= len(e.faces) {
		return nil
	}
	return e.faces[i]
}

// addFace remembers a face read from a font file.
func (e *androidEngine) addFace(f *font.Face) int {
	i := len(e.faces)
	e.faces = append(e.faces, &androidFont{face: f, upem: float32(f.Upem())})
	e.faceIndex[f] = i
	return i
}

// readFile returns the face at index of a font file, reading and parsing
// the file once.
func (e *androidEngine) readFile(path string, index int) (int, bool) {
	key := androidFileKey{path, index}
	if i, ok := e.index[key]; ok {
		return i, i >= 0
	}
	faces, ok := e.fileFaces[path]
	if !ok {
		faces = parseFontFile(path)
		e.fileFaces[path] = faces
	}
	i := -1
	if index >= 0 && index < len(faces) {
		i = e.addFace(faces[index])
	}
	e.index[key] = i
	return i, i >= 0
}

// parseFontFile reads a font file the system has and parses it: its first
// font when it holds one, every font of a collection when it holds many.
func parseFontFile(path string) []*font.Face {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if f, err := font.ParseTTF(bytes.NewReader(data)); err == nil {
		return []*font.Face{f}
	}
	if faces, err := font.ParseTTC(bytes.NewReader(data)); err == nil {
		return faces
	}
	return nil
}

// parseFontData parses font bytes the app added.
func parseFontData(data []byte) []*font.Face {
	if f, err := font.ParseTTF(bytes.NewReader(data)); err == nil {
		return []*font.Face{f}
	}
	if faces, err := font.ParseTTC(bytes.NewReader(data)); err == nil {
		return faces
	}
	return nil
}

// matchFont asks Android's own font matcher for the font of a family that
// covers text, and returns its file and its place in the file. family ""
// is the system's own font; text must not be empty.
//
// The matcher and the font it returns are freed before returning: the path
// is copied out of the font first, because libandroid keeps the font's
// file open until AFont_close.
func matchFont(family string, weight int, italic bool, text []uint16) (path string, index int, ok bool) {
	if len(text) == 0 {
		return "", 0, false
	}
	m := C.AFontMatcher_create()
	if m == nil {
		return "", 0, false
	}
	defer C.AFontMatcher_destroy(m)
	C.AFontMatcher_setStyle(m, C.uint16_t(min(max(weight, 0), 1000)), C.bool(italic))
	name := C.CString(family)
	defer C.free(unsafe.Pointer(name))
	// The text goes through C memory: no pointer of Go's is passed to C.
	buf := C.malloc(C.size_t(len(text)) * C.size_t(unsafe.Sizeof(C.uint16_t(0))))
	if buf == nil {
		return "", 0, false
	}
	defer C.free(buf)
	units := unsafe.Slice((*C.uint16_t)(buf), len(text))
	for i, u := range text {
		units[i] = C.uint16_t(u)
	}
	f := C.AFontMatcher_match(m, name, (*C.uint16_t)(buf), C.uint32_t(len(text)), nil)
	if f == nil {
		return "", 0, false
	}
	defer C.AFont_close(f)
	return C.GoString(C.AFont_getFontFilePath(f)), int(C.AFont_getCollectionIndex(f)), true
}

// systemFonts returns the fonts Android has, enumerating them once and
// freeing the iterator and every font it hands out.
func (e *androidEngine) systemFonts() []androidSystemFont {
	if e.enumerated {
		return e.system
	}
	e.enumerated = true
	it := C.ASystemFontIterator_open()
	if it == nil {
		return nil
	}
	defer C.ASystemFontIterator_close(it)
	for {
		f := C.ASystemFontIterator_next(it)
		if f == nil {
			break
		}
		// The path is valid only until AFont_close: copy it out first.
		path := C.GoString(C.AFont_getFontFilePath(f))
		index := int(C.AFont_getCollectionIndex(f))
		weight := int(C.AFont_getWeight(f))
		italic := bool(C.AFont_isItalic(f))
		C.AFont_close(f)
		if path != "" {
			e.system = append(e.system, androidSystemFont{path, index, weight, italic})
		}
	}
	return e.system
}

// familyOfFile returns the family a font file declares, reading its name
// table once, or "".
func (e *androidEngine) familyOfFile(path string) string {
	if e.families == nil {
		e.families = map[string]string{}
	}
	if name, ok := e.families[path]; ok {
		return name
	}
	name := ""
	if data, err := os.ReadFile(path); err == nil {
		if ld, err := ot.NewLoader(bytes.NewReader(data)); err == nil {
			desc, _ := font.Describe(ld, nil)
			name = desc.Family
		}
	}
	e.families[path] = name
	return name
}

// systemFace returns the face of a family Android has, at a weight and
// italic. Android's font matcher would find the font too, but it cannot
// say whether it has a family at all, which a list of families needs.
//
// The families of the system's font files are read as they are asked
// about: a family Android has is found reading the few files that declare
// it, a family it does not have costs reading the name table of every
// system font, once.
func (e *androidEngine) systemFace(family string, weight int, italic bool) (int, bool) {
	name := strings.ToLower(family)
	var best androidSystemFont
	bestDist, found := 0, false
	for _, sf := range e.systemFonts() {
		if strings.ToLower(e.familyOfFile(sf.file)) != name {
			continue
		}
		dist := absInt(sf.weight - weight)
		if sf.italic != italic {
			dist += 200
		}
		if !found || dist < bestDist {
			best, bestDist, found = sf, dist, true
		}
	}
	if !found {
		return 0, false
	}
	return e.readFile(best.file, best.index)
}

// defaultFace returns the face of the system's own font, or -1.
func (e *androidEngine) defaultFace() int {
	if path, index, ok := matchFont("", 400, false, []uint16{' '}); ok {
		if i, ok := e.readFile(path, index); ok {
			return i
		}
	}
	return -1
}

// findFace returns the face of the first family of a style Android has, or
// the system's own font when it has none.
func (e *androidEngine) findFace(family string, weight int, italic bool) int {
	for _, f := range familyList(family) {
		if faces, ok := e.app[strings.ToLower(f)]; ok && len(faces) > 0 {
			if i := e.closestApp(faces, weight, italic); i >= 0 {
				return i
			}
		}
		switch g := generic(f); g {
		case "system-ui":
			if path, index, ok := matchFont("", weight, italic, []uint16{' '}); ok {
				if i, ok := e.readFile(path, index); ok {
					return i
				}
			}
		case "sans-serif", "serif", "monospace":
			if path, index, ok := matchFont(g, weight, italic, []uint16{' '}); ok {
				if i, ok := e.readFile(path, index); ok {
					return i
				}
			}
		default:
			if i, ok := e.systemFace(f, weight, italic); ok {
				return i
			}
		}
	}
	return e.defaultFace()
}

// closestApp returns the face of an app's fonts closest to a weight and
// italic: Android's matcher does not know the app's fonts, so the choice
// is made here, from what each font says of itself.
func (e *androidEngine) closestApp(faces []int, weight int, italic bool) int {
	best, bestDist := -1, 0
	for _, i := range faces {
		af := e.faceAt(i)
		if af == nil {
			continue
		}
		aspect := af.face.Describe().Aspect
		dist := int(math.Abs(float64(aspect.Weight) - float64(weight)))
		if (aspect.Style == font.StyleItalic) != italic {
			dist += 200
		}
		if best < 0 || dist < bestDist {
			best, bestDist = i, dist
		}
	}
	return best
}

// faceForStyle returns the face of a style.
func (e *androidEngine) faceForStyle(style Style) int {
	key := androidStyleKey{style.Family, style.weight(), style.Italic}
	if i, ok := e.styleFaces[key]; ok {
		return i
	}
	i := e.findFace(style.Family, style.weight(), style.Italic)
	if len(e.styleFaces) >= 1024 {
		clear(e.styleFaces)
	}
	e.styleFaces[key] = i
	return i
}

// fontOf returns the Font of a face at a size, or nil.
func (e *androidEngine) fontOf(face int, size float32) *Font {
	af := e.faceAt(face)
	if af == nil {
		return nil
	}
	key := androidFontKey{face, size}
	if f, ok := e.fonts[key]; ok {
		return f
	}
	// Per em: font units to DIPs.
	p := size / af.upem
	f := &Font{Size: size, native: uintptr(face)}
	if ext, ok := af.face.FontHExtents(); ok {
		f.Ascent, f.Descent = ext.Ascender*p, -ext.Descender*p
		f.LineGap = max(ext.LineGap*p, 0)
	}
	f.underlineTop = af.face.LineMetric(font.UnderlinePosition) * p
	f.underlineThick = af.face.LineMetric(font.UnderlineThickness) * p
	f.strikeTop = af.face.LineMetric(font.StrikethroughPosition) * p
	f.strikeThick = af.face.LineMetric(font.StrikethroughThickness) * p
	if f.Ascent <= 0 {
		f.Ascent, f.Descent = size*0.8, size*0.2
	}
	// Underlines a tenth of an em below the baseline, strikethroughs
	// through the middle of an x, for fonts that do not say.
	if f.underlineThick <= 0 {
		f.underlineTop, f.underlineThick = -size/10, max(size/14, 1)
	}
	if f.strikeThick <= 0 {
		f.strikeTop, f.strikeThick = size*0.3, max(size/14, 1)
	}
	if len(e.fonts) >= maxFonts*2 {
		clear(e.fonts)
	}
	e.fonts[key] = f
	return f
}

func (e *androidEngine) font(style Style) *Font {
	return e.fontOf(e.faceForStyle(style), style.FontSize())
}

// fallbackFace returns the face Android's font matcher picks for a rune a
// style's own font lacks, or -1.
func (e *androidEngine) fallbackFace(family string, weight int, italic bool, r rune) int {
	key := androidFallbackKey{family, r}
	if i, ok := e.fallbacks[key]; ok {
		return i
	}
	i := -1
	if path, index, ok := matchFont(family, weight, italic, utf16Of(r)); ok {
		if j, ok := e.readFile(path, index); ok {
			i = j
		}
	}
	if len(e.fallbacks) >= 4096 {
		clear(e.fallbacks)
	}
	e.fallbacks[key] = i
	return i
}

// utf16Of returns a rune as UTF-16 units.
func utf16Of(r rune) []uint16 {
	if r >= 0x10000 && r <= 0x10FFFF {
		r -= 0x10000
		return []uint16{uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)}
	}
	if r > 0xFFFF || (r >= 0xD800 && r < 0xE000) {
		r = 0xFFFD
	}
	return []uint16{uint16(r)}
}

// androidFontmap resolves the face of each rune of a text in a style, as
// the segmenter splits the text into runs: the style's own font, or the
// system's fallback for what it lacks.
type androidFontmap struct {
	e      *androidEngine
	family string
	face   int
	weight int
	italic bool
}

func (m androidFontmap) ResolveFace(r rune) *font.Face {
	e := m.e
	own := e.faceAt(m.face)
	if own != nil {
		if _, ok := own.face.NominalGlyph(r); ok {
			return own.face
		}
	}
	if af := e.faceAt(e.fallbackFace(m.family, m.weight, m.italic, r)); af != nil {
		return af.face
	}
	// Android's matcher always answers with a font of the system, so this
	// is the style's own font only for a rune no font covers: it draws
	// tofu, as Android draws it.
	if own != nil {
		return own.face
	}
	if af := e.faceAt(e.defaultFace()); af != nil {
		return af.face
	}
	return nil
}

// absInt returns the distance between two weights.
func absInt(d int) int {
	if d < 0 {
		return -d
	}
	return d
}

// shapingFeatures turns a style's OpenType features into go-text's.
func shapingFeatures(list string) []shaping.FontFeature {
	fs := features(list)
	if len(fs) == 0 {
		return nil
	}
	out := make([]shaping.FontFeature, len(fs))
	for i, f := range fs {
		out[i] = shaping.FontFeature{Tag: ot.NewTag(f.tag[0], f.tag[1], f.tag[2], f.tag[3]), Value: f.value}
	}
	return out
}

func (e *androidEngine) shape(text []rune, style Style, spans []Span, width float32, rtl, wholeWords bool) []shapedLine {
	if len(text) == 0 {
		return nil
	}
	// The style of each rune: spans', then the text's.
	styles := make([]Style, 0, min(len(spans)+1, 255))
	at := make([]uint8, len(text))
	{
		start := 0
		for _, sp := range spans {
			end := min(sp.End, len(text))
			if end <= start {
				continue
			}
			for i := start; i < end; i++ {
				at[i] = uint8(len(styles))
			}
			styles = append(styles, sp.style(style))
			start = end
			if len(styles) == 255 {
				break
			}
		}
		for i := start; i < len(text); i++ {
			at[i] = uint8(len(styles))
		}
		styles = append(styles, style)
	}
	dir := di.DirectionLTR
	if rtl {
		dir = di.DirectionRTL
	}
	policy := shaping.WhenNecessary
	if wholeWords {
		policy = shaping.Never
	}
	// Shape each run of one style: the segmenter splits it by direction,
	// script and face, and the shaper positions its glyphs, their clusters
	// the runes of the paragraph.
	var outs []shaping.Output
	for start := 0; start < len(text); {
		end := start + 1
		for end < len(text) && at[end] == at[start] {
			end++
		}
		st := styles[at[start]]
		f := e.font(st)
		if f == nil { // no font of the system could be read
			return nil
		}
		in := shaping.Input{
			Text:         text,
			RunStart:     start,
			RunEnd:       end,
			Direction:    dir,
			Size:         fixed.Int26_6(st.FontSize()*64 + 0.5),
			FontFeatures: shapingFeatures(st.Features),
		}
		fm := androidFontmap{e: e, family: st.Family, face: int(f.native), weight: st.weight(), italic: st.Italic}
		for _, piece := range e.segment.Split(in, fm) {
			out := e.shaper.Shape(piece)
			if sp := fixed.Int26_6(st.LetterSpacing*64 + 0.5); sp != 0 {
				spaceOut(&out, sp)
			}
			outs = append(outs, out)
		}
		start = end
	}
	if len(outs) == 0 {
		return nil
	}
	// Lines: the wrapper breaks them between the words, or within one
	// longer than a line unless wholeWords, and orders the runs of each
	// line visually. Whitespace ending a line keeps its advance, which the
	// layout trims, as the other engines of the package keep it.
	maxWidth := fixed.Int26_6(1 << 25)
	if width > 0 {
		maxWidth = fixed.Int26_6(width*64 + 0.5)
	}
	config := shaping.WrapConfig{Direction: dir, BreakPolicy: policy, DisableTrailingWhitespaceTrim: true}
	lines, _ := e.wrap.WrapParagraphF(config, maxWidth, text, shaping.NewSliceIterator(outs))

	out := make([]shapedLine, 0, len(lines))
	order := make([]int, 0, len(outs))
	for _, ln := range lines {
		if len(ln) == 0 {
			continue
		}
		order = order[:0]
		for i := range ln {
			order = append(order, i)
		}
		// The runs of a line come in logical order, with their visual one
		// in VisualIndex; a line is drawn left to right in visual order.
		sort.SliceStable(order, func(a, b int) bool { return ln[order[a]].VisualIndex < ln[order[b]].VisualIndex })
		sl := shapedLine{start: math.MaxInt32}
		x := float32(0)
		for _, k := range order {
			run := ln[k]
			f := e.fontOf(e.faceAt2(run.Face), float32(run.Size)/64)
			if f == nil {
				continue
			}
			sr := shapedRun{font: f, start: run.Runes.Offset, end: run.Runes.Offset + run.Runes.Count}
			sl.start, sl.end = min(sl.start, sr.start), max(sl.end, sr.end)
			// The run's glyphs come in visual order, their advances along
			// the pen, their offsets off it.
			pen := float32(0)
			for _, g := range run.Glyphs {
				adv := float32(g.Advance) / 64
				if g.GlyphID != font.EmptyGlyph {
					sr.glyphs = append(sr.glyphs, Glyph{
						Font: f, ID: uint32(g.GlyphID),
						X: x + pen + float32(g.XOffset)/64, Y: float32(g.YOffset) / 64,
						Advance: adv, Cluster: g.ClusterIndex,
						RTL: run.Direction == di.DirectionRTL,
					})
				}
				pen += adv
			}
			if len(sr.glyphs) > 0 {
				sl.runs = append(sl.runs, sr)
			}
			x += pen
		}
		if len(sl.runs) > 0 {
			out = append(out, sl)
		}
	}
	return out
}

// faceAt2 returns the place of a parsed face in faces, or -1.
func (e *androidEngine) faceAt2(f *font.Face) int {
	if i, ok := e.faceIndex[f]; ok {
		return i
	}
	return -1
}

// spaceOut adds a letter spacing after each cluster of a shaped run, which
// its shaper does not know.
func spaceOut(out *shaping.Output, spacing fixed.Int26_6) {
	for i := 0; i < len(out.Glyphs); {
		j := i + 1
		for j < len(out.Glyphs) && out.Glyphs[j].ClusterIndex == out.Glyphs[i].ClusterIndex {
			j++
		}
		g := &out.Glyphs[j-1]
		g.Advance += spacing
		g.XAdvance += spacing
		i = j
	}
	out.RecomputeAdvance()
}

// glyph rasterizes a glyph's outline with go-text's, at scale pixels per
// DIP, its origin dx pixels right of the left edge of a pixel.
//
// Android's own glyphs come from Skia, which is not in the NDK: the
// coverage is rasterized here from the font's outline, and the text is
// drawn without subpixel antialiasing, as Android's own UI draws it
// (TextParams says so), so a shade and subpixel are not asked of it.
func (e *androidEngine) glyph(f *Font, id uint32, scale, dx float32, _ Shade, _ bool) bitmap {
	if f == nil || id == uint32(font.EmptyGlyph) {
		return bitmap{}
	}
	af := e.faceAt(int(f.native))
	if af == nil || af.upem <= 0 || f.Size <= 0 || scale <= 0 {
		return bitmap{}
	}
	outline, ok := af.face.GlyphDataOutline(font.GID(id))
	if !ok || len(outline.Segments) == 0 {
		return bitmap{} // a space, or a glyph of a color font this engine does not draw
	}
	// Font units to pixels; the outline's Y grows up, the bitmap's down.
	s := f.Size * scale / af.upem
	minX, minY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxX, maxY := float32(-math.MaxFloat32), float32(-math.MaxFloat32)
	for i := range outline.Segments {
		for _, p := range outline.Segments[i].ArgsSlice() {
			minX, maxX = min(minX, p.X), max(maxX, p.X)
			minY, maxY = min(minY, p.Y), max(maxY, p.Y)
		}
	}
	// The box holds the control points, and so the curves between them.
	left := int(math.Floor(float64(minX*s + dx)))
	right := int(math.Ceil(float64(maxX*s + dx)))
	top := int(math.Floor(float64(-maxY * s)))
	bottom := int(math.Ceil(float64(-minY * s)))
	w, h := right-left, bottom-top
	if w <= 0 || h <= 0 || w > 2048 || h > 2048 {
		return bitmap{}
	}
	z := vector.NewRasterizer(w, h)
	z.DrawOp = draw.Src
	at := func(p ot.SegmentPoint) (float32, float32) {
		return p.X*s + dx - float32(left), -p.Y*s - float32(top)
	}
	for i := range outline.Segments {
		seg := &outline.Segments[i]
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			x, y := at(seg.Args[0])
			z.MoveTo(x, y)
		case ot.SegmentOpLineTo:
			x, y := at(seg.Args[0])
			z.LineTo(x, y)
		case ot.SegmentOpQuadTo:
			bx, by := at(seg.Args[0])
			cx, cy := at(seg.Args[1])
			z.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := at(seg.Args[0])
			cx, cy := at(seg.Args[1])
			dxx, dyy := at(seg.Args[2])
			z.CubeTo(bx, by, cx, cy, dxx, dyy)
		}
	}
	// The contours of a glyph outline close on themselves.
	z.ClosePath()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	z.Draw(mask, mask.Bounds(), image.White, image.Point{})
	return bitmap{left: left, top: top, w: w, h: h, pix: mask.Pix}
}

// positions puts the glyphs of a line at four positions within a pixel,
// the nearest to the pen: Skia, which draws Android's own text, places
// them that way.
func (e *androidEngine) positions(*Font, float32, bool) (int, bool) { return 4, true }

// baseline is the whole pixel nearest y, as Skia rounds a baseline: only
// the horizontal position is subpixel.
func (e *androidEngine) baseline(y float32) float32 { return float32(math.Round(float64(y))) }

// join: Skia blends each glyph of a run in turn, as renderers do.
func (e *androidEngine) join() bool { return false }

// textParams leaves coverage as it is, without subpixel antialiasing.
func (e *androidEngine) textParams() (scene.TextParams, bool) { return scene.TextParams{}, false }

// decorate places underlines and strikethroughs along the advances of the
// runs, at the positions and thicknesses of their fonts, whole pixels from
// the baseline.
func (e *androidEngine) decorate(r decoRange) []Stroke {
	s := r.scale
	base := e.baseline(r.baseline() * s)
	var out []Stroke
	r.runs(func(f *Font, i, j int) {
		x0, x1 := r.span(i, j)
		top, thick := f.strikeTop, f.strikeThick
		if r.d == Underline {
			top, thick = f.underlineTop, f.underlineThick
		}
		t := float32(math.Round(float64(base - top*s)))
		st := Stroke{X0: (r.x + x0) * s, X1: (r.x + x1) * s, Top: t, Bottom: t + max(float32(math.Round(float64(thick*s))), 1)}
		if n := len(out); n > 0 && out[n-1].Top == st.Top && out[n-1].Bottom == st.Bottom {
			out[n-1].X1 = max(out[n-1].X1, st.X1)
			return
		}
		out = append(out, st)
	})
	return out
}

// register adds the fonts of a font file the app has, under family, or
// under their own family names when family is "".
//
// Android's own text stack would need a font provider for the app's fonts;
// this engine reads them itself, so a registered font is the face of the
// family it is registered under in findFace.
func (e *androidEngine) register(data []byte, family string) error {
	faces := parseFontData(data)
	if len(faces) == 0 {
		return errors.New("mygo: this font is not one the text engine reads")
	}
	if family == "" {
		seen := map[string]bool{}
		for _, f := range faces {
			name := f.Describe().Family
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			e.app[strings.ToLower(name)] = append(e.app[strings.ToLower(name)], e.addFace(f))
		}
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(family))
	for _, f := range faces {
		e.app[key] = append(e.app[key], e.addFace(f))
	}
	return nil
}

// fontCount returns how many Fonts the engine holds, for the System to let
// them go when they are many.
func (e *androidEngine) fontCount() int { return len(e.fonts) }

// forgetFonts lets go of the Fonts, and of the faces of a style and a
// fallback that named them: what a font file holds stays, since reading it
// again would cost a read of the file.
func (e *androidEngine) forgetFonts() {
	clear(e.fonts)
	clear(e.styleFaces)
	clear(e.fallbacks)
}
