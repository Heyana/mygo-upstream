// Tests for the platform features of the Android backend.
//
// They run on the desktop CI with CGO_ENABLED=0 (see platform.go's header):
// platform.go has no build constraint and the Android-only files of this
// package do not, so `go test ./internal/android/backend/` compiles this file
// together with the logic it tests and nothing else — no JNI, no Java.
//
// Everything here talks to a fake PlatformBridge. That is the seam that makes
// the Android platform features testable at all: what the Java side does with a
// call (ClipboardManager, an ACTION_VIEW intent, a notification, a node
// provider) can only be checked on a device, but what the Go side decides
// before the call — which format maps to which MIME type, which URL is refused,
// which error a failed notification becomes, what JSON a tree serializes to —
// is exactly what these tests pin down.
package backend

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/transfer"
)

// fakeBridge records what the Go side asks the Java side to do.
type fakeBridge struct {
	payload ClipboardPayload
	mimes   []string
	readErr error

	writeErr error
	writes   []ClipboardPayload
	clears   int
	clearErr error

	openErr error
	opened  []string

	allowed bool
	postErr error
	posts   []*platform.Notification

	cancels    []string
	cancelAll  int
	announce   []string
	trees      []string
	treeErr    error
	announceNo int
}

func (f *fakeBridge) ClipboardRead() (ClipboardPayload, []string, error) {
	return f.payload, f.mimes, f.readErr
}

func (f *fakeBridge) ClipboardWrite(p ClipboardPayload) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, p)
	return nil
}

func (f *fakeBridge) ClipboardClear() error {
	f.clears++
	return f.clearErr
}

func (f *fakeBridge) OpenExternalURL(url string) error {
	if f.openErr != nil {
		return f.openErr
	}
	f.opened = append(f.opened, url)
	return nil
}

func (f *fakeBridge) NotificationsAllowed() bool { return f.allowed }

func (f *fakeBridge) PostNotification(n *platform.Notification) error {
	if f.postErr != nil {
		return f.postErr
	}
	f.posts = append(f.posts, n)
	return nil
}

func (f *fakeBridge) CancelNotification(id string) error {
	f.cancels = append(f.cancels, id)
	return nil
}

func (f *fakeBridge) CancelAllNotifications() error {
	f.cancelAll++
	return nil
}

func (f *fakeBridge) AnnounceAs(text string) error {
	f.announceNo++
	f.announce = append(f.announce, text)
	return nil
}

func (f *fakeBridge) SetAccessibilityTreeJSON(json string) error {
	if f.treeErr != nil {
		return f.treeErr
	}
	f.trees = append(f.trees, json)
	return nil
}

// install puts b in place of the bridge for the duration of the test and
// returns it. The bridge is package state, so every test that installs one
// restores the previous value; tests in this package must not run in parallel.
func install(t *testing.T, b PlatformBridge) {
	t.Helper()
	before := platformBridge()
	SetPlatformBridge(b)
	t.Cleanup(func() { SetPlatformBridge(before) })
}

// withoutBridge makes sure nothing is installed, for the tests that check what
// happens when the Java side never attached.
func withoutBridge(t *testing.T) {
	t.Helper()
	install(t, nil)
}

func TestPlatformClipboardReadData(t *testing.T) {
	tests := []struct {
		name    string
		bridge  *fakeBridge
		formats []transfer.Format
		want    []transfer.Format
		wantErr error
	}{
		{
			name:   "text and html",
			bridge: &fakeBridge{payload: ClipboardPayload{Plain: "hi", HasPlain: true, HTML: "<b>hi</b>", HasHTML: true}, mimes: []string{"text/plain", "text/html"}},
			want:   []transfer.Format{transfer.Text, transfer.HTML},
		},
		{
			name:    "only the requested format",
			bridge:  &fakeBridge{payload: ClipboardPayload{Plain: "hi", HasPlain: true, HTML: "<b>hi</b>", HasHTML: true}, mimes: []string{"text/plain", "text/html"}},
			formats: []transfer.Format{transfer.HTML},
			want:    []transfer.Format{transfer.HTML},
		},
		{
			name:    "a format the clipboard cannot carry",
			bridge:  &fakeBridge{payload: ClipboardPayload{Plain: "hi", HasPlain: true}, mimes: []string{"text/plain"}},
			formats: []transfer.Format{transfer.PNG},
			wantErr: transfer.ErrFormat,
		},
		{
			// The clip holds something — an image — that this backend does
			// not decode: an unavailable representation, not an empty
			// clipboard, which is what the desktop backends report for the
			// same shape.
			name:    "an image clip has nothing this backend can read",
			bridge:  &fakeBridge{mimes: []string{"image/png"}},
			wantErr: transfer.ErrFormat,
		},
		{
			name:   "an empty clipboard is empty data, not a missing format",
			bridge: &fakeBridge{},
		},
		{
			name:    "a read that failed is reported",
			bridge:  &fakeBridge{readErr: errors.New("no")},
			wantErr: errors.New("no"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			install(t, tt.bridge)
			data, err := androidClipboard{}.ReadData(tt.formats)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("ReadData() = %v, want error", data.Formats())
				}
				if errors.Is(tt.wantErr, transfer.ErrFormat) && !errors.Is(err, transfer.ErrFormat) {
					t.Fatalf("ReadData() error = %v, want transfer.ErrFormat", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadData() error = %v", err)
			}
			if got := data.Formats(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ReadData() formats = %v, want %v", got, tt.want)
			}
			if len(tt.want) > 0 {
				if b, err := data.Read(tt.want[0]); err != nil || len(b) == 0 {
					t.Fatalf("ReadData() rep %v = %q, %v", tt.want[0], b, err)
				}
			}
		})
	}
}

func TestPlatformClipboardWithoutBridge(t *testing.T) {
	withoutBridge(t)
	c := androidClipboard{}
	if got := c.ReadText(); got != "" {
		t.Errorf("ReadText() = %q, want %q", got, "")
	}
	if got := c.ReadHTML(); got != "" {
		t.Errorf("ReadHTML() = %q, want %q", got, "")
	}
	if got := c.Formats(); got != nil {
		t.Errorf("Formats() = %v, want nil", got)
	}
	if got := c.AvailableFormats(); got != nil {
		t.Errorf("AvailableFormats() = %v, want nil", got)
	}
	if _, err := c.ReadData(nil); !errors.Is(err, ErrPlatformBridge) {
		t.Errorf("ReadData() error = %v, want ErrPlatformBridge", err)
	}
	if err := c.WriteData(transfer.TextData("x"), nil); !errors.Is(err, ErrPlatformBridge) {
		t.Errorf("WriteData() error = %v, want ErrPlatformBridge", err)
	}
}

func TestPlatformClipboardWriteData(t *testing.T) {
	tests := []struct {
		name         string
		bridge       *fakeBridge
		data         transfer.Data
		wantPlain    string
		wantHasPlain bool
		wantHTML     string
		wantHasHTML  bool
		wantWrite    bool
		wantErr      error
		released     bool
	}{
		{
			name:         "text",
			bridge:       &fakeBridge{},
			data:         transfer.TextData("hello"),
			wantPlain:    "hello",
			wantHasPlain: true,
			wantWrite:    true,
			released:     true,
		},
		{
			name:         "text and html of one item",
			bridge:       &fakeBridge{},
			data:         transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte("hello")), transfer.Bytes(transfer.HTML, []byte("<b>hello</b>")))),
			wantPlain:    "hello",
			wantHasPlain: true,
			wantHTML:     "<b>hello</b>",
			wantHasHTML:  true,
			wantWrite:    true,
			released:     true,
		},
		{
			name:        "html only",
			bridge:      &fakeBridge{},
			data:        transfer.New(transfer.NewItem(transfer.Bytes(transfer.HTML, []byte("<i>x</i>")))),
			wantHTML:    "<i>x</i>",
			wantHasHTML: true,
			wantWrite:   true,
			released:    true,
		},
		{
			name:    "an image is not something this clipboard can hold",
			bridge:  &fakeBridge{},
			data:    transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("png")))),
			wantErr: platform.ErrUnsupported,
		},
		{
			name:      "empty data clears the clipboard",
			bridge:    &fakeBridge{},
			data:      transfer.Data{},
			wantWrite: true,
			released:  true,
		},
		{
			name:   "a failed write keeps the providers",
			bridge: &fakeBridge{writeErr: errors.New("denied")},
			data:   transfer.TextData("hello"),
			// wantWrite is false: the fake records nothing on failure.
			wantErr: errors.New("denied"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			install(t, tt.bridge)
			released := false
			err := androidClipboard{}.WriteData(tt.data, func() { released = true })
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("WriteData() = nil, want error")
				}
				if errors.Is(tt.wantErr, platform.ErrUnsupported) && !errors.Is(err, platform.ErrUnsupported) {
					t.Fatalf("WriteData() error = %v, want platform.ErrUnsupported", err)
				}
				if released {
					t.Fatalf("WriteData() released the providers after a failed write")
				}
				if len(tt.bridge.writes) != 0 {
					t.Fatalf("WriteData() wrote %v after failing", tt.bridge.writes)
				}
				return
			}
			if err != nil {
				t.Fatalf("WriteData() error = %v", err)
			}
			if released != tt.released {
				t.Fatalf("released = %v, want %v", released, tt.released)
			}
			if tt.wantWrite != (len(tt.bridge.writes) == 1) {
				t.Fatalf("writes = %v, want %d write(s)", tt.bridge.writes, boolToInt(tt.wantWrite))
			}
			if !tt.wantWrite {
				return
			}
			got := tt.bridge.writes[0]
			if got.Plain != tt.wantPlain || got.HasPlain != tt.wantHasPlain {
				t.Errorf("plain = %q/%v, want %q/%v", got.Plain, got.HasPlain, tt.wantPlain, tt.wantHasPlain)
			}
			if got.HTML != tt.wantHTML || got.HasHTML != tt.wantHasHTML {
				t.Errorf("html = %q/%v, want %q/%v", got.HTML, got.HasHTML, tt.wantHTML, tt.wantHasHTML)
			}
		})
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestPlatformClipboardFormats(t *testing.T) {
	install(t, &fakeBridge{mimes: []string{"text/plain; charset=utf-8", "image/png", "TEXT/HTML"}})
	c := androidClipboard{}
	if got, want := c.Formats(), []transfer.Format{transfer.Text, transfer.HTML}; !reflect.DeepEqual(got, want) {
		t.Errorf("Formats() = %v, want %v", got, want)
	}
	if got, want := c.AvailableFormats(), []string{"text/plain; charset=utf-8", "image/png", "TEXT/HTML"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AvailableFormats() = %v, want the platform's own names %v", got, want)
	}
	if got := c.ReadText(); got != "" {
		t.Errorf("ReadText() = %q, want %q for a clip with no text", got, "")
	}
}

func TestPlatformClipboardClear(t *testing.T) {
	f := &fakeBridge{}
	install(t, f)
	androidClipboard{}.Clear()
	if f.clears != 1 {
		t.Fatalf("Clear() asked the bridge %d times, want 1", f.clears)
	}
}

// TestPlatformClipboardRoundTrip writes through the Go side into the fake bridge
// and reads back through it, which is what the JNI pair does on a device: the
// point is that the representation the caller wrote is the one it reads.
func TestPlatformClipboardRoundTrip(t *testing.T) {
	f := &fakeBridge{}
	install(t, f)
	c := androidClipboard{}
	c.WriteText("emoji: 🚀 and a tab\there")
	if len(f.writes) != 1 || !f.writes[0].HasPlain || f.writes[0].HasHTML {
		t.Fatalf("writes = %+v, want one plain-text write", f.writes)
	}
	f.payload, f.mimes = f.writes[0], []string{"text/plain"}
	if got, want := c.ReadText(), "emoji: 🚀 and a tab\there"; got != want {
		t.Fatalf("ReadText() = %q, want %q", got, want)
	}
	if got := c.ReadHTML(); got != "" {
		t.Fatalf("ReadHTML() = %q, want %q", got, "")
	}
	c.WriteHTML("<b>x</b>")
	if len(f.writes) != 2 || f.writes[1].HasPlain || !f.writes[1].HasHTML {
		t.Fatalf("writes = %+v, want a second HTML-only write", f.writes)
	}
}

func TestPlatformOpenExternal(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "https", url: "https://example.com/a?b=1#c", want: true},
		{name: "http", url: "http://example.com", want: true},
		{name: "mailto", url: "mailto:someone@example.com", want: true},
		{name: "tel", url: "tel:+8613800000000", want: true},
		{name: "an upper-case scheme is the same scheme", url: "HTTPS://EXAMPLE.COM/x", want: true},
		{name: "empty", url: ""},
		{name: "no scheme", url: "//example.com/path"},
		{name: "plain text", url: "example.com"},
		{name: "javascript", url: "javascript:alert(1)"},
		{name: "data", url: "data:text/html,<script>x</script>"},
		{name: "file", url: "file:///data/local/tmp/x"},
		{name: "content", url: "content://com.example.provider/x"},
		{name: "intent", url: "intent://scan/#Intent;scheme=zxing;package=com.x;end"},
		{name: "android-app", url: "android-app://com.example/path"},
		{name: "package", url: "package:com.example"},
		{name: "market", url: "market://details?id=com.example"},
		{name: "ftp", url: "ftp://example.com/x"},
		{name: "http with no host", url: "https:"},
		{name: "http with an empty authority", url: "https:///path"},
		{name: "mailto with no address", url: "mailto:"},
		{name: "a newline", url: "https://example.com/\nnext"},
		{name: "a carriage return", url: "https://example.com/\rX"},
		{name: "a NUL", url: "https://example.com/\x00"},
		{name: "leading whitespace", url: " https://example.com"},
		{name: "trailing whitespace", url: "https://example.com "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeBridge{}
			install(t, f)
			err := androidShell{}.OpenExternal(tt.url)
			if tt.want {
				if err != nil {
					t.Fatalf("OpenExternal(%q) error = %v", tt.url, err)
				}
				if len(f.opened) != 1 || f.opened[0] != tt.url {
					t.Fatalf("opened = %v, want the URL unchanged (%q)", f.opened, tt.url)
				}
				return
			}
			if err == nil {
				t.Fatalf("OpenExternal(%q) = nil, want a refusal", tt.url)
			}
			if len(f.opened) != 0 {
				t.Fatalf("opened = %v, want nothing handed to the platform", f.opened)
			}
		})
	}
}

func TestPlatformShellWithoutBridge(t *testing.T) {
	withoutBridge(t)
	sh := androidShell{}
	if err := sh.OpenExternal("https://example.com"); !errors.Is(err, ErrPlatformBridge) {
		t.Errorf("OpenExternal() error = %v, want ErrPlatformBridge", err)
	}
	// The desktop-only calls are unsupported on Android whether the bridge is
	// attached or not: Android has no desktop shell to answer them.
	if err := sh.OpenPath("/data/x"); !errors.Is(err, platform.ErrUnsupported) {
		t.Errorf("OpenPath() error = %v, want platform.ErrUnsupported", err)
	}
	if err := sh.TrashItem("/data/x"); !errors.Is(err, platform.ErrUnsupported) {
		t.Errorf("TrashItem() error = %v, want platform.ErrUnsupported", err)
	}
	sh.ShowItemInFolder("/data/x")
	sh.Beep()
}

func TestPlatformNotificationsSupported(t *testing.T) {
	withoutBridge(t)
	if androidNotificationsSupported() {
		t.Errorf("androidNotificationsSupported() = true without a bridge")
	}
	install(t, &fakeBridge{})
	if !androidNotificationsSupported() {
		t.Errorf("androidNotificationsSupported() = false with a bridge")
	}
}

func TestPlatformShowNotification(t *testing.T) {
	denied := errors.New("the platform refused it")
	tests := []struct {
		name    string
		bridge  *fakeBridge
		wantErr error
		wantUp  bool
	}{
		{name: "allowed", bridge: &fakeBridge{allowed: true}, wantUp: true},
		{name: "the user does not allow notifications", bridge: &fakeBridge{allowed: false}, wantErr: platform.ErrNotificationsDenied},
		{name: "the post failed", bridge: &fakeBridge{allowed: true, postErr: denied}, wantErr: denied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			install(t, tt.bridge)
			var got error
			calls := 0
			androidShowNotification(&platform.Notification{ID: "n1", Title: "t"}, func(err error) {
				calls++
				got = err
			})
			if calls != 1 {
				t.Fatalf("done ran %d times, want exactly once", calls)
			}
			if tt.wantErr == nil {
				if got != nil {
					t.Fatalf("done(%v), want nil", got)
				}
				if len(tt.bridge.posts) != 1 {
					t.Fatalf("posted %d notifications, want 1", len(tt.bridge.posts))
				}
				return
			}
			if !errors.Is(got, tt.wantErr) {
				t.Fatalf("done(%v), want %v", got, tt.wantErr)
			}
			if len(tt.bridge.posts) != 0 {
				t.Fatalf("posted %v after a failure", tt.bridge.posts)
			}
		})
	}
}

func TestPlatformShowNotificationWithoutBridge(t *testing.T) {
	withoutBridge(t)
	var got error
	androidShowNotification(&platform.Notification{}, func(err error) { got = err })
	if !errors.Is(got, ErrPlatformBridge) {
		t.Fatalf("done(%v), want ErrPlatformBridge", got)
	}
}

func TestPlatformRemoveNotifications(t *testing.T) {
	f := &fakeBridge{allowed: true}
	install(t, f)
	androidRemoveNotification("n1")
	androidRemoveAllNotifications()
	if !reflect.DeepEqual(f.cancels, []string{"n1"}) {
		t.Errorf("cancels = %v, want [n1]", f.cancels)
	}
	if f.cancelAll != 1 {
		t.Errorf("cancelAll = %d, want 1", f.cancelAll)
	}
	// Without a bridge the calls do nothing instead of panicking: the pump may
	// remove notifications while the Java side is gone.
	withoutBridge(t)
	androidRemoveNotification("n1")
	androidRemoveAllNotifications()
}

func TestPlatformNotificationClick(t *testing.T) {
	// No sink: the tap is reported as undelivered rather than vanishing.
	SetNotificationClickSink(nil)
	if DispatchNotificationClicked("n1") {
		t.Fatalf("DispatchNotificationClicked() = true with no sink")
	}
	var got []string
	SetNotificationClickSink(func(id string) { got = append(got, id) })
	t.Cleanup(func() { SetNotificationClickSink(nil) })
	if !DispatchNotificationClicked("n1") {
		t.Fatalf("DispatchNotificationClicked() = false with a sink")
	}
	if !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("clicked = %v, want [n1]", got)
	}
}

func TestPlatformAccessTreeJSON(t *testing.T) {
	tree := &platform.AccessTree{
		Focus:         2,
		Announcements: []string{"Saved"},
		Nodes: []platform.AccessNode{
			{
				ID: 1, Parent: -1, Role: platform.RoleGroup, Label: "Toolbar",
				Bounds:  platform.RectF{X: 0, Y: 0, W: 100, H: 32},
				States:  platform.AccessFocusable | platform.AccessVertical,
				Actions: platform.ActionPress,
			},
			{
				ID: 2, Parent: 0, Role: platform.RoleHeading, Label: "Settings",
				Bounds: platform.RectF{X: 4, Y: 40, W: 200, H: 24},
				Level:  1, PosInSet: 1, SetSize: 3,
			},
		},
	}
	const want = `{"focus":2,"nodes":[` +
		`{"id":1,"parent":-1,"role":"group","label":"Toolbar","bounds":[0,0,100,32],` +
		`"states":["focusable","vertical"],"actions":["press"],` +
		`"min":0,"max":0,"now":0,"step":0,"level":0,"posInSet":0,"setSize":0,"selStart":0,"selEnd":0},` +
		`{"id":2,"parent":0,"role":"heading","label":"Settings","bounds":[4,40,200,24],` +
		`"min":0,"max":0,"now":0,"step":0,"level":1,"posInSet":1,"setSize":3,"selStart":0,"selEnd":0}],` +
		`"announcements":["Saved"]}`

	got, err := encodeAccessTree(tree)
	if err != nil {
		t.Fatalf("encodeAccessTree() error = %v", err)
	}
	if got != want {
		t.Fatalf("encodeAccessTree() =\n%s\nwant\n%s", got, want)
	}

	// The same tree through the bridge, which is the path a device takes.
	f := &fakeBridge{}
	install(t, f)
	if err := updateAccessibilityTree(tree); err != nil {
		t.Fatalf("updateAccessibilityTree() error = %v", err)
	}
	if len(f.trees) != 1 || f.trees[0] != want {
		t.Fatalf("the bridge got %v, want the encoded tree", f.trees)
	}
}

func TestPlatformAccessTreeJSONIsParsable(t *testing.T) {
	tree := &platform.AccessTree{
		Nodes: []platform.AccessNode{
			{ID: 7, Parent: -1, Role: platform.RoleSlider, Label: "Volume", Bounds: platform.RectF{X: 1, Y: 2, W: 3, H: 4},
				Min: 0, Max: 10, Now: 4, Step: 1, Value: "4", Description: "the speaker", Placeholder: "none",
				States: platform.AccessDisabled | platform.AccessInvalid, Actions: platform.ActionIncrement | platform.ActionDecrement,
				SelStart: -1, SelEnd: -1},
		},
	}
	got, err := encodeAccessTree(tree)
	if err != nil {
		t.Fatalf("encodeAccessTree() error = %v", err)
	}
	var parsed struct {
		Focus uint64 `json:"focus"`
		Nodes []struct {
			ID          uint64     `json:"id"`
			Parent      int        `json:"parent"`
			Role        string     `json:"role"`
			Label       string     `json:"label"`
			Value       string     `json:"value"`
			Description string     `json:"description"`
			Placeholder string     `json:"placeholder"`
			Bounds      [4]float64 `json:"bounds"`
			States      []string   `json:"states"`
			Actions     []string   `json:"actions"`
			Min         float64    `json:"min"`
			Max         float64    `json:"max"`
			Now         float64    `json:"now"`
			Step        float64    `json:"step"`
			SelStart    int        `json:"selStart"`
			SelEnd      int        `json:"selEnd"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("the tree is not valid JSON: %v\n%s", err, got)
	}
	if len(parsed.Nodes) != 1 {
		t.Fatalf("parsed %d nodes, want 1", len(parsed.Nodes))
	}
	n := parsed.Nodes[0]
	if n.ID != 7 || n.Parent != -1 || n.Role != "slider" || n.Label != "Volume" || n.Value != "4" {
		t.Errorf("node = %+v", n)
	}
	if n.Bounds != [4]float64{1, 2, 3, 4} || n.Min != 0 || n.Max != 10 || n.Now != 4 || n.Step != 1 {
		t.Errorf("node = %+v", n)
	}
	if !reflect.DeepEqual(n.States, []string{"disabled", "invalid"}) {
		t.Errorf("states = %v, want [disabled invalid]", n.States)
	}
	if !reflect.DeepEqual(n.Actions, []string{"increment", "decrement"}) {
		t.Errorf("actions = %v, want [increment decrement]", n.Actions)
	}
	if n.SelStart != -1 || n.SelEnd != -1 {
		t.Errorf("selection = %d/%d, want -1/-1 preserved", n.SelStart, n.SelEnd)
	}
	if n.Description != "the speaker" || n.Placeholder != "none" {
		t.Errorf("description/placeholder = %q/%q", n.Description, n.Placeholder)
	}
}

func TestPlatformAccessTreeEmpty(t *testing.T) {
	got, err := encodeAccessTree(&platform.AccessTree{})
	if err != nil {
		t.Fatalf("encodeAccessTree() error = %v", err)
	}
	// "nodes" is a list, never null: the Java side reads it with a loop.
	if got != `{"focus":0,"nodes":[]}` {
		t.Fatalf("encodeAccessTree() = %s, want {\"focus\":0,\"nodes\":[]}", got)
	}
}

func TestPlatformAccessTreeNilIsAnError(t *testing.T) {
	if _, err := encodeAccessTree(nil); err == nil {
		t.Fatalf("encodeAccessTree(nil) = nil error, want a refusal")
	}
}

func TestPlatformUpdateAccessibilityTreeWithoutBridge(t *testing.T) {
	withoutBridge(t)
	if err := updateAccessibilityTree(&platform.AccessTree{}); !errors.Is(err, ErrPlatformBridge) {
		t.Fatalf("updateAccessibilityTree() error = %v, want ErrPlatformBridge", err)
	}
}

func TestPlatformAnnounce(t *testing.T) {
	f := &fakeBridge{}
	install(t, f)
	if err := announceAs("Saved"); err != nil {
		t.Fatalf("announceAs() error = %v", err)
	}
	if err := announceAs(""); err != nil {
		t.Fatalf("announceAs(\"\") error = %v", err)
	}
	if !reflect.DeepEqual(f.announce, []string{"Saved"}) {
		t.Fatalf("announced %v, want [Saved]: an empty announcement is nothing to say", f.announce)
	}
	withoutBridge(t)
	if err := announceAs("Saved"); !errors.Is(err, ErrPlatformBridge) {
		t.Fatalf("announceAs() error = %v, want ErrPlatformBridge", err)
	}
}

func TestPlatformAccessAction(t *testing.T) {
	SetAccessActionSink(nil)
	if DispatchAccessAction(3, platform.AccessPress) {
		t.Fatalf("DispatchAccessAction() = true with no sink")
	}
	type performed struct {
		id     uint64
		action platform.AccessActionKind
	}
	var seen []performed
	SetAccessActionSink(sinkFunc(func(id uint64, action platform.AccessActionKind) {
		seen = append(seen, performed{id, action})
	}))
	t.Cleanup(func() { SetAccessActionSink(nil) })
	if !DispatchAccessAction(3, platform.AccessFocus) {
		t.Fatalf("DispatchAccessAction() = false with a sink")
	}
	if !reflect.DeepEqual(seen, []performed{{3, platform.AccessFocus}}) {
		t.Fatalf("sink saw %v, want [{3 focus}]", seen)
	}
}

// sinkFunc adapts a function to AccessActionSink.
type sinkFunc func(id uint64, action platform.AccessActionKind)

func (f sinkFunc) AccessAction(id uint64, action platform.AccessActionKind) { f(id, action) }

// TestPlatformAccessNames pins the vocabulary that crosses to Java: every role,
// state and action has a name, every name is unique, and a name that is not in
// the table is refused rather than guessed at.
func TestPlatformAccessNames(t *testing.T) {
	roles := map[string]platform.AccessRole{}
	for role := platform.RoleGroup; role <= platform.RoleHeading; role++ {
		name := accessRoleName(role)
		if name == "" {
			t.Errorf("role %d has no name", role)
			continue
		}
		if other, dup := roles[name]; dup {
			t.Errorf("roles %d and %d share the name %q", other, role, name)
		}
		roles[name] = role
	}
	if len(roles) != len(accessRoles) {
		t.Errorf("%d roles named, the table has %d entries", len(roles), len(accessRoles))
	}

	// Every state bit and every action bit is named: a bit with no name would
	// reach TalkBack as a state that silently is not there.
	for _, s := range []platform.AccessStates{
		platform.AccessFocusable, platform.AccessDisabled, platform.AccessChecked, platform.AccessMixed,
		platform.AccessSelected, platform.AccessExpanded, platform.AccessMultiline, platform.AccessPassword,
		platform.AccessReadOnly, platform.AccessSelectable, platform.AccessOffscreen, platform.AccessMultiselectable,
		platform.AccessSegment, platform.AccessSearch, platform.AccessInvalid, platform.AccessSortAscending,
		platform.AccessSortDescending, platform.AccessExpandable, platform.AccessVertical,
	} {
		if got := accessStateNames(s); len(got) != 1 {
			t.Errorf("state %d names = %v, want one name", s, got)
		}
	}
	for _, a := range []platform.AccessActions{
		platform.ActionPress, platform.ActionFocus, platform.ActionIncrement, platform.ActionDecrement,
		platform.ActionSetValue, platform.ActionScrollIntoView, platform.ActionExpand,
	} {
		if got := accessActionNames(a); len(got) != 1 {
			t.Errorf("action %d names = %v, want one name", a, got)
		}
	}
	if got := accessStateNames(0); got != nil {
		t.Errorf("accessStateNames(0) = %v, want nil", got)
	}
	if got := accessActionNames(0); got != nil {
		t.Errorf("accessActionNames(0) = %v, want nil", got)
	}

	for _, k := range []platform.AccessActionKind{
		platform.AccessPress, platform.AccessFocus, platform.AccessIncrement, platform.AccessDecrement,
		platform.AccessSetValue, platform.AccessScrollIntoView, platform.AccessExpand, platform.AccessCollapse,
	} {
		name, ok := accessActionKindName(k)
		if !ok {
			t.Errorf("action kind %d has no name", k)
			continue
		}
		if back, ok := accessActionKind(name); !ok || back != k {
			t.Errorf("accessActionKind(%q) = %v, %v, want %v", name, back, ok, k)
		}
	}
	if _, ok := accessActionKind("nonsense"); ok {
		t.Errorf("accessActionKind(\"nonsense\") parsed")
	}
	if _, ok := accessActionKind(""); ok {
		t.Errorf("accessActionKind(\"\") parsed")
	}
}

// accessActionKindName is the table lookup the test above needs: the production
// side only ever goes name → kind (Java sends the name), so the reverse mapping
// is written here rather than made part of the wire contract.
func accessActionKindName(k platform.AccessActionKind) (string, bool) {
	for _, a := range accessActionKinds {
		if a.action == k {
			return a.name, true
		}
	}
	return "", false
}

// TestPlatformMIMEMapping pins the two representations this clipboard can
// carry: a format outside them is not mapped, and a MIME type outside them is
// not read.
func TestPlatformMIMEMapping(t *testing.T) {
	for _, tt := range []struct {
		format transfer.Format
		mime   string
		ok     bool
	}{
		{transfer.Text, "text/plain", true},
		{transfer.HTML, "text/html", true},
		{transfer.PNG, "", false},
		{transfer.URIList, "", false},
		{transfer.FileList, "", false},
		{"application/json", "", false},
	} {
		mime, ok := androidMIME(tt.format)
		if ok != tt.ok || mime != tt.mime {
			t.Errorf("androidMIME(%q) = %q, %v, want %q, %v", tt.format, mime, ok, tt.mime, tt.ok)
		}
	}
	for _, tt := range []struct {
		mime   string
		format transfer.Format
		ok     bool
	}{
		{"text/plain", transfer.Text, true},
		{"text/html", transfer.HTML, true},
		{"text/plain; charset=utf-8", transfer.Text, true},
		{"  TEXT/PLAIN ", transfer.Text, true},
		{"image/png", "", false},
		{"", "", false},
		{"text/", "", false},
	} {
		format, ok := formatForMIME(tt.mime)
		if ok != tt.ok || format != tt.format {
			t.Errorf("formatForMIME(%q) = %q, %v, want %q, %v", tt.mime, format, ok, tt.format, tt.ok)
		}
	}
}

// TestPlatformValidateExternalURLKeepsTheString checks the property the security
// note in OpenExternal depends on: what was validated is what is handed over,
// byte for byte, so no re-encoding can change the intent's data after the check.
func TestPlatformValidateExternalURLKeepsTheString(t *testing.T) {
	const raw = "https://example.com/a%2Fb?q=%20#frag"
	got, err := validateExternalURL(raw)
	if err != nil {
		t.Fatalf("validateExternalURL(%q) error = %v", raw, err)
	}
	if got != raw {
		t.Fatalf("validateExternalURL() = %q, want the input unchanged (%q)", got, raw)
	}
	if !strings.Contains(got, "%2F") {
		t.Fatalf("the escape was rewritten: %q", got)
	}
}
