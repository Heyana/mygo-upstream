# Startup

`ui.Startup` holds an application-defined startup view until the window's
first frame has been presented and the application marks itself ready.
It then crossfades to the content. Both views use ordinary MyGo UI components;
the application owns branding, layout and its readiness condition.

```go
startup := &ui.Startup{Duration: 600 * time.Millisecond}

view := func(c *ui.Context) {
    startup.View(c, func() {
        ui.Column(c).Fill().Center().Children(func() {
            ui.Image(c, logo).Size(120, 120)
            ui.Text(c, "My App").FontSize(32).Bold()
        })
    }, func() {
        app.contentView(c)
    })
}

// After initial application data has loaded, on the UI thread:
startup.Ready = true
window.Invalidate()
```

Keep `Startup` across frames. A zero `Duration` uses 200ms; `Ease` defaults
to `ui.EaseInOut`. System Reduce Motion switches immediately, even if it is
enabled during a fade. Content is built when the transition begins and cannot
receive pointer, keyboard or accessibility actions until it ends.

The transition runs once per window and keyed component. Changing `Ready`
back to false does not replay it. Give sibling startup components distinct
`c.Key` values. Set the root background as for other views; colors inherit
the current theme.

On native surfaces, presentation is acknowledged by the window, rather than
inferred from frame submission. Headless tests acknowledge their rendered
first frame. The operating system's pre-Go launch placeholder remains a
separate packaging concern; `Startup` manages the UI after Go starts.
