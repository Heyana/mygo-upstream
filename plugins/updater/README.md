# updater

The update window of [MyGo](https://github.com/egoist/mygo) apps, in the
manner of Sparkle: it checks for updates in the background, shows the
release notes of a new version and offers to install it, skip it or remind
the user later, downloads it with a progress bar and relaunches the app into
it. It is all Go, with no npm package.

```go
import "github.com/egoist/mygo/plugins/updater"

mygo.Use(updater.Plugin) // or updater.New(updater.Options{Interval: ..., Icon: ...})
```

and "Check for Updates…" in the app's menu:

```go
{Label: "My App", Submenu: []*mygo.MenuItem{
	{Role: mygo.RoleAbout},
	updater.MenuItem(),
	mygo.Separator(),
	{Role: mygo.RoleQuit},
}},
```

Apps whose windows all show native UI use `native.Plugin` (package
`github.com/egoist/mygo/plugins/updater/native`) instead: the same window
drawn in native UI, with no webview.

An authenticated application controller can call
`updater.InstallUpdate(ctx, selectedVersion)` off the UI thread after checking
with `mygo.Updater.Check`. It shares the update window's session and signed
installer, joins a download of the same release, and preserves automatic-download
preferences. It reports installation completion without relaunching; the app
can then report success and use `mygo.App.Relaunch`. Canceling a waiting remote
caller does not cancel an install that the local window already started.

Apps with background helpers that outlive the GUI can call
`mygo.Updater.SetKeepPreviousBundles(true)` before enabling updates on macOS.
Replaced bundles stay next to the application, and the app owns their cleanup
after the helpers exit. The default continues to remove replaced bundles.

It speaks the user's language (English, Chinese, Dutch, French, German,
Italian, Japanese, Korean, Polish, Portuguese, Russian, Spanish, Turkish,
Ukrainian), and `Options.Strings` changes its texts or adds languages.

The app must be built with updates: see
[auto-updates](https://github.com/egoist/mygo/blob/main/docs/updates.md).
[The plugin's documentation](https://github.com/egoist/mygo/blob/main/docs/plugins/updater.md)
lists the options, the languages and the preferences
(`SetAutomaticChecks`, `SetAutomaticDownloads`, and `OnChange` to hear
them change) apps can offer.
