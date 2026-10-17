//go:build android && cgo

package backend

/*
#include <GLES3/gl3.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"log"
	"strings"
	"unsafe"
)

// presenter puts the frame the ui package drew in memory onto the display.
//
// Android takes the CPU path (see ui/gpu_other.go), so a frame arrives here as
// premultiplied BGRA rows and has to become a GL texture. Two things are worth
// knowing before touching this file:
//
//   - OpenGL ES has no GL_BGRA external format at all. Component swizzling on
//     upload is not something the spec permits — the GL_EXT_texture_format_BGRA8888
//     extension exists on some drivers and is exactly the kind of thing that
//     works until the phone changes. So the channel swap happens on lookup,
//     with GL_TEXTURE_SWIZZLE_RGBA (ES 3.0 core): the GPU does it for free and
//     the upload stays one copy. Doing it in Go would mean walking every pixel
//     on the render thread, once per frame.
//   - The triangle samples past the texture on the diagonal, so the wrap mode
//     must clamp; with the default REPEAT the opposite edge folds back in.
//
// The GL objects live as long as the EGL context, which is the process: the
// context survives every surface change (see eglState), so nothing here needs
// to be rebuilt when the Activity goes to the background and back.
type presenter struct {
	ready bool
	prog  C.GLuint
	tex   C.GLuint
	loc   C.GLint // the sampler uniform

	// texW/texH are the size the texture already holds, so a same-size frame
	// re-uploads into it instead of asking the driver for a new allocation.
	texW, texH int
}

const presenterVS = `#version 300 es
// One triangle that covers the viewport, derived from gl_VertexID: no vertex
// buffer, no matrices, and no attribute state left enabled by someone else.
out vec2 uv;
void main() {
	vec2 p = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));
	uv = p;
	gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}
`

const presenterFS = `#version 300 es
precision mediump float;
in vec2 uv;
uniform sampler2D frame;
out vec4 color;
void main() {
	// The rows arrive top-down and a GL texture is addressed bottom-up, so the
	// flip belongs here rather than in a row-by-row copy on upload.
	color = texture(frame, vec2(uv.x, 1.0 - uv.y));
}
`

// draw uploads pix and shows it. It reports false when there is no frame to
// show, in which case the caller clears instead.
func (p *presenter) draw(pix []byte, stride, width, height int) bool {
	if width <= 0 || height <= 0 || stride < width*4 || len(pix) < stride*height {
		return false
	}
	if err := p.ensure(); err != nil {
		log.Printf("mygo/android: %v", err)
		return false
	}
	C.glActiveTexture(C.GL_TEXTURE0)
	C.glBindTexture(C.GL_TEXTURE_2D, p.tex)
	// Rows are stride bytes apart, which is width*4 only when the renderer drew
	// exactly the visible width; UNPACK_ROW_LENGTH is how ES 3 says so.
	C.glPixelStorei(C.GL_UNPACK_ALIGNMENT, 4)
	C.glPixelStorei(C.GL_UNPACK_ROW_LENGTH, C.GLint(stride/4))
	if p.texW != width || p.texH != height {
		C.glTexImage2D(C.GL_TEXTURE_2D, 0, C.GL_RGBA8,
			C.GLsizei(width), C.GLsizei(height), 0,
			C.GL_RGBA, C.GL_UNSIGNED_BYTE, unsafe.Pointer(&pix[0]))
		p.texW, p.texH = width, height
	} else {
		C.glTexSubImage2D(C.GL_TEXTURE_2D, 0, 0, 0,
			C.GLsizei(width), C.GLsizei(height),
			C.GL_RGBA, C.GL_UNSIGNED_BYTE, unsafe.Pointer(&pix[0]))
	}
	C.glPixelStorei(C.GL_UNPACK_ROW_LENGTH, 0) // leave no state behind
	// Replace rather than blend: the frame is the whole surface, alpha included.
	C.glDisable(C.GL_BLEND)
	C.glUseProgram(p.prog)
	C.glUniform1i(p.loc, 0)
	C.glDrawArrays(C.GL_TRIANGLES, 0, 3)
	return true
}

func (p *presenter) ensure() error {
	if p.ready {
		return nil
	}
	vs, err := compileShader(C.GL_VERTEX_SHADER, presenterVS)
	if err != nil {
		return err
	}
	defer C.glDeleteShader(vs)
	fs, err := compileShader(C.GL_FRAGMENT_SHADER, presenterFS)
	if err != nil {
		return err
	}
	defer C.glDeleteShader(fs)
	prog := C.glCreateProgram()
	C.glAttachShader(prog, vs)
	C.glAttachShader(prog, fs)
	C.glLinkProgram(prog)
	var ok C.GLint
	C.glGetProgramiv(prog, C.GL_LINK_STATUS, &ok)
	if ok != C.GL_TRUE {
		log.Printf("mygo/android: present program link failed: %s", programLog(prog))
		C.glDeleteProgram(prog)
		return fmt.Errorf("present: link failed")
	}
	p.prog = prog
	name := C.CString("frame")
	p.loc = C.glGetUniformLocation(prog, (*C.GLchar)(unsafe.Pointer(name)))
	C.free(unsafe.Pointer(name))
	C.glGenTextures(1, &p.tex)
	C.glBindTexture(C.GL_TEXTURE_2D, p.tex)
	// NEAREST: the frame is already at device resolution, so any filtering is
	// resampling on purpose and would only blur it.
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_MIN_FILTER, C.GL_NEAREST)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_MAG_FILTER, C.GL_NEAREST)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_WRAP_S, C.GL_CLAMP_TO_EDGE)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_WRAP_T, C.GL_CLAMP_TO_EDGE)
	// ES 3.0 has the swizzle state per channel only — GL_TEXTURE_SWIZZLE_RGBA
	// (the set-all-four form) arrived with desktop GL 3.3 / GL_ARB_texture_swizzle
	// and is not in an ES 3.0 header.
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_SWIZZLE_R, C.GL_BLUE)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_SWIZZLE_G, C.GL_GREEN)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_SWIZZLE_B, C.GL_RED)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_SWIZZLE_A, C.GL_ALPHA)
	p.ready = true
	return nil
}

func compileShader(kind C.GLenum, src string) (C.GLuint, error) {
	sh := C.glCreateShader(kind)
	csrc := C.CString(src)
	defer C.free(unsafe.Pointer(csrc))
	clen := C.GLint(len(src))
	C.glShaderSource(sh, 1, (**C.GLchar)(unsafe.Pointer(&csrc)), &clen)
	C.glCompileShader(sh)
	var ok C.GLint
	C.glGetShaderiv(sh, C.GL_COMPILE_STATUS, &ok)
	if ok != C.GL_TRUE {
		msg := shaderLog(sh)
		C.glDeleteShader(sh)
		return 0, fmt.Errorf("present: shader compile failed: %s", msg)
	}
	return sh, nil
}

func shaderLog(sh C.GLuint) string {
	var n C.GLint
	C.glGetShaderiv(sh, C.GL_INFO_LOG_LENGTH, &n)
	if n <= 1 {
		return "(no log)"
	}
	buf := make([]byte, int(n))
	C.glGetShaderInfoLog(sh, n, nil, (*C.GLchar)(unsafe.Pointer(&buf[0])))
	return strings.TrimRight(string(buf), "\x00")
}

func programLog(prog C.GLuint) string {
	var n C.GLint
	C.glGetProgramiv(prog, C.GL_INFO_LOG_LENGTH, &n)
	if n <= 1 {
		return "(no log)"
	}
	buf := make([]byte, int(n))
	C.glGetProgramInfoLog(prog, n, nil, (*C.GLchar)(unsafe.Pointer(&buf[0])))
	return strings.TrimRight(string(buf), "\x00")
}
