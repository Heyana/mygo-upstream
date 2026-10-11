package vt

import (
	"bytes"
	"encoding/xml"
	"io"
	"unsafe"
)

// Ghostty's VT formatter preserves the active OSC 8 state, but currently
// emits cell hyperlinks only in HTML. Pair both formats by exact text offsets
// rather than matching labels, which can repeat with different destinations.
func snapshotLinks(data, html []byte, cursorURL ...string) []byte {
	if !bytes.Contains(html, []byte("<a ")) {
		return data
	}
	type link struct {
		start, end int
		url        string
	}
	var text []byte
	var links []link
	var pages []int
	url := ""
	d := xml.NewDecoder(bytes.NewReader(html))
	for {
		token, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return data
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "a" {
				for _, a := range token.Attr {
					if a.Name.Local == "href" {
						url = a.Value
					}
				}
			}
		case xml.EndElement:
			if token.Name.Local == "a" {
				url = ""
			}
			if token.Name.Local == "div" {
				pages = append(pages, len(text))
			}
		case xml.CharData:
			start := len(text)
			text = append(text, token...)
			if url != "" && len(text) > start {
				links = append(links, link{start, len(text), url})
			}
		}
	}
	// Locate only printable bytes and line feeds, leaving all original VT
	// control sequences intact. OSC/DCS payloads are never terminal text.
	var plain []byte
	var positions []int
	for i := 0; i < len(data); {
		if data[i] == 0x1b {
			i = snapshotEscapeEnd(data, i)
			continue
		}
		if data[i] >= 0x20 || data[i] == '\n' || data[i] == '\t' {
			plain = append(plain, data[i])
			positions = append(positions, i)
		}
		i++
	}
	// HTML div boundaries contribute a visual newline. Account for it only
	// when the VT text has one: a soft wrap can continue across native pages.
	pages = append(pages, len(text))
	type segment struct{ start, end, delta int }
	var segments []segment
	start, at := 0, 0
	for _, end := range pages {
		chunk := text[start:end]
		if !bytes.HasPrefix(plain[at:], chunk) {
			if at >= len(plain) || plain[at] != '\n' || !bytes.HasPrefix(plain[at+1:], chunk) {
				return data // Never guess a link destination from a label.
			}
			at++
		}
		segments = append(segments, segment{start, end, at - start})
		at += len(chunk)
		start = end
	}
	insert := map[int][]byte{}
	for _, link := range links {
		for _, s := range segments {
			if link.start < s.start || link.start >= s.end || link.end > s.end {
				continue
			}
			a, b := link.start+s.delta, link.end+s.delta
			if a >= len(positions) || b > len(positions) {
				return data
			}
			begin := positions[a]
			if a > 0 {
				begin = positions[a-1] + 1
			}
			insert[begin] = append(insert[begin], []byte("\x1b]8;;"+link.url+"\x1b\\")...)
			insert[positions[b-1]+1] = append(insert[positions[b-1]+1], []byte("\x1b]8;;\x1b\\")...)
			break
		}
	}
	// Restoring a pending-wrap cursor reprints its cell after the body. That
	// cell needs its own URL before the formatter restores the active OSC state.
	if len(cursorURL) > 0 && cursorURL[0] != "" && at < len(positions) {
		insert[positions[at]] = append(insert[positions[at]], []byte("\x1b]8;;"+cursorURL[0]+"\x1b\\")...)
		end := positions[len(positions)-1] + 1
		insert[end] = append(insert[end], []byte("\x1b]8;;\x1b\\")...)
	}
	var out bytes.Buffer
	for i, b := range data {
		out.Write(insert[i])
		out.WriteByte(b)
	}
	out.Write(insert[len(data)])
	return out.Bytes()
}

func (t *Terminal) snapshotCursorLink() string {
	const dataCursorPendingWrap = 5
	var pending bool
	if t.get(dataCursorPendingWrap, unsafe.Pointer(&pending)) != nil || !pending {
		return ""
	}
	x, y := t.Cursor()
	ref := GridRef{size: unsafe.Sizeof(GridRef{})}
	if terminalGridRef(t.h, point{tag: 0, x: uint16(x), y: uint32(y)}, &ref) != 0 {
		return ""
	}
	return t.Hyperlink(ref)
}

func snapshotEscapeEnd(data []byte, i int) int {
	i++
	if i >= len(data) {
		return i
	}
	kind := data[i]
	i++
	switch kind {
	case '[':
		for i < len(data) {
			b := data[i]
			i++
			if b >= 0x40 && b <= 0x7e {
				break
			}
		}
	case ']', 'P', '_', '^', 'X':
		for i < len(data) {
			if data[i] == '\a' {
				return i + 1
			}
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 2
			}
			i++
		}
	default:
		if kind >= 0x20 && kind <= 0x2f {
			for i < len(data) {
				b := data[i]
				i++
				if b >= 0x30 && b <= 0x7e {
					break
				}
			}
		}
	}
	return i
}
