//go:build android && cgo

package backend

import (
	"log"
	"sync"

	"github.com/egoist/mygo/internal/platform"
)

// Clipboard returns the Android clipboard.
func (b *Backend) Clipboard() platform.Clipboard {
	b.wirePlatform()
	return androidClipboard{}
}

// Shell returns the Android shell.
func (b *Backend) Shell() platform.Shell {
	b.wirePlatform()
	return androidShell{}
}

// NotificationsSupported reports that the platform has notifications; whether
// the user allows them is a different question, answered by ShowNotification
// (platform.ErrNotificationsDenied) rather than folded into this.
func (b *Backend) NotificationsSupported() bool {
	b.wirePlatform()
	return androidNotificationsSupported()
}

// ShowNotification posts n and reports the outcome to done.
func (b *Backend) ShowNotification(n *platform.Notification, done func(error)) {
	b.wirePlatform()
	androidShowNotification(n, done)
}

// RemoveNotification removes one of the app's notifications.
func (b *Backend) RemoveNotification(id string) { androidRemoveNotification(id) }

// RemoveAllNotifications removes the app's notifications.
func (b *Backend) RemoveAllNotifications() { androidRemoveAllNotifications() }

// OnNotificationClicked delivers a tapped notification to the app handler, on
// the pump, where every handler callback belongs.
func (b *Backend) OnNotificationClicked(id string) {
	b.Post(func() {
		if h := b.handler(); h != nil {
			h.NotificationClicked(id)
		}
	})
}

// UpdateAccessibility pushes the tree to the Java node provider, which is what
// TalkBack reads (see MygoPlatform.kt).
func (b *Backend) UpdateAccessibility(tree *platform.AccessTree) {
	if err := updateAccessibilityTree(tree); err != nil {
		log.Printf("mygo/android: accessibility: %v", err)
	}
}

// Announce reads text out through the platform's accessibility announcement.
func (b *Backend) Announce(text string) {
	if err := announceAs(text); err != nil {
		log.Printf("mygo/android: accessibility announcement: %v", err)
	}
}

var wirePlatformOnce sync.Once

// wirePlatform installs the notification-click delivery once, whichever of the
// overridden methods is called first (the Backend instance is what the sink
// needs, and New() is not a file this line owns).
func (b *Backend) wirePlatform() {
	wirePlatformOnce.Do(func() { SetNotificationClickSink(b.OnNotificationClicked) })
}
