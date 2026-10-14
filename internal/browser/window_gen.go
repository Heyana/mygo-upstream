//go:build js && wasm

package browser

// This file is generated from internal/platform/platform.go by
// .workbuddy/gen_window.py: the Window and Page methods the browser backend
// stubs. Edit the generator, not this file.

import "github.com/egoist/mygo/internal/platform"

func (w *window) SetMinimumSize(s platform.Size) {}

func (w *window) SetMaximumSize(s platform.Size) {}

func (w *window) SetResizable(v bool) {}

func (w *window) IsResizable() bool {
	return true
}

func (w *window) SetMovable(v bool) {}

func (w *window) IsMovable() bool {
	return true
}

func (w *window) SetMinimizable(v bool) {}

func (w *window) IsMinimizable() bool {
	return true
}

func (w *window) SetMaximizable(v bool) {}

func (w *window) IsMaximizable() bool {
	return true
}

func (w *window) SetClosable(v bool) {}

func (w *window) IsClosable() bool {
	return true
}

func (w *window) SetAlwaysOnTop(v bool) {}

func (w *window) IsAlwaysOnTop() bool {
	return false
}

func (w *window) Minimize() {}

func (w *window) IsMinimized() bool {
	return false
}

func (w *window) Maximize() {}

func (w *window) Unmaximize() {}

func (w *window) IsMaximized() bool {
	return false
}

func (w *window) Restore() {}

func (w *window) SetFullScreen(v bool) {}

func (w *window) IsFullScreen() bool {
	return false
}

func (w *window) Center() {}

func (w *window) SetBackgroundColor(c platform.Color) {}

func (w *window) SetOpacity(v float64) {}

func (w *window) Opacity() float64 {
	return 1
}

func (w *window) SetHasShadow(v bool) {}

func (w *window) HasShadow() bool {
	return true
}

func (w *window) SetIgnoreMouseEvents(v bool) {}

func (w *window) SetContentProtection(v bool) {}

func (w *window) SetVibrancy(material string) {}

func (w *window) SetProgressBar(state string, value float64) {}

func (w *window) FlashFrame(flash bool) {}

func (w *window) SetSkipTaskbar(v bool) {}

func (w *window) SetVisibleOnAllWorkspaces(v bool) {}

func (w *window) SetIcon(png []byte) error {
	return nil
}

func (w *window) SetMenu(m *platform.Menu) {}

func (w *window) SetAutoHideMenu(v bool) {}

func (w *window) TitleBar() platform.TitleBar {
	panic("mygo: not implemented in the browser")
}

// SafeArea is zero: a page in a browser is laid out in the document, and
// whatever chrome the browser puts around it (a toolbar, a phone's status
// bar) is outside the canvas rather than over it — so the content is already
// inside the safe area and padding would only add a margin the page did not
// ask for.
func (w *window) SafeArea() platform.SafeArea { return platform.SafeArea{} }

func (w *window) StartDrag() {}

func (w *window) TitleBarDoubleClicked() {}

// PlaceFlyout does nothing: the browser backend has one window and no
// work area to place a flyout against. Kept because the Window interface
// has it, and generated like its neighbours — the generator is
// .workbuddy/gen_window.py, which is not in the repository.

func (w *window) PlaceFlyout(f platform.Flyout, size platform.Size) {}

// Page is the window's web page. The browser backend shows Content, so
// the page methods do nothing.

func (w *window) WebViewHandle() uintptr {
	return 2
}

func (w *window) LoadURL(url string) {}

func (w *window) LoadHTML(html, baseURL string) {}

func (w *window) LoadFile(path, readAccessDir string) {}

func (w *window) Reload(ignoreCache bool) {}

func (w *window) StopLoading() {}

func (w *window) GoBack() {}

func (w *window) GoForward() {}

func (w *window) CanGoBack() bool {
	return false
}

func (w *window) CanGoForward() bool {
	return false
}

func (w *window) URL() string {
	return ""
}

func (w *window) IsLoading() bool {
	return false
}

func (w *window) Eval(js string) {}

func (w *window) CallAsyncFunction(body string, cb func(result string, err error)) {
	cb("", errNoPage)
}

func (w *window) SetZoom(factor float64) {}

func (w *window) Zoom() float64 {
	return 1
}

func (w *window) SetUserAgent(ua string) {}

func (w *window) UserAgent() string {
	return ""
}

func (w *window) OpenDevTools() {}

func (w *window) CloseDevTools() {}

func (w *window) IsDevToolsOpened() bool {
	return false
}

func (w *window) CapturePage(cb func(png []byte, err error)) {
	cb(nil, errNoPage)
}

func (w *window) PrintToPDF(opts platform.PDFOptions, cb func(pdf []byte, err error)) {
	cb(nil, errNoPage)
}

func (w *window) DroppedFiles() []string {
	return nil
}

func (w *window) Print() {}
