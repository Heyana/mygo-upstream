package ui

import "time"

// Startup shows an application-defined startup view until the first frame
// has been presented and Ready is true, then crossfades to its content.
// Configure it and change Ready on the UI thread; after changing Ready,
// invalidate the window as for other view state. The transition is one-shot
// and kept separately for each window and keyed component.
//
//	startup := &ui.Startup{Duration: 600 * time.Millisecond}
//	// In the window's view:
//	startup.View(c, func() { app.launchView(c) }, func() { app.contentView(c) })
//	// After initial application data has loaded:
//	startup.Ready = true
//	window.Invalidate()
type Startup struct {
	// Ready reports application readiness. Once the transition has started,
	// setting it false does not replay the startup view.
	Ready bool
	// Duration is the crossfade duration; zero uses 200ms.
	Duration time.Duration
	// Ease defaults to EaseInOut. Reduce Motion always switches immediately,
	// including when enabled in the middle of the transition.
	Ease Easing
}

type startupState struct {
	started, finished bool
}

// View builds splash until startup is ready, then crossfades to content.
// Both builders use ordinary UI components and inherit the current theme.
// Content is inert until the transition completes, including its keyboard
// shortcuts and accessibility actions. The returned box fills its parent.
func (s *Startup) coreView(c *context, splash, content func()) *node {
	if s.Duration < 0 {
		panic("ui.Startup: Duration must not be negative")
	}
	stage := coreBox(c).Fill()
	stage.widget = "Startup"
	state := coreLocal(stage, "startup", func() startupState { return startupState{} })
	if !state.started && s.Ready && c.rt.host.firstFramePresented() {
		state.started = true
	} else if !state.started {
		// Subscribe even when application readiness is still pending.
		c.rt.host.firstFramePresented()
	}
	opacity := float32(0)
	if !state.finished {
		target := float32(1)
		if state.started {
			target = 0
		}
		duration, ease := s.Duration, s.Ease
		if duration == 0 {
			duration = defaultDuration
		}
		if ease == nil {
			ease = EaseInOut
		}
		if state.started && c.rt.preferences().ReduceMotion {
			state.finished = true
		} else {
			opacity = stage.AnimateWith("startup", target, duration, ease)
			state.finished = state.started && opacity == 0
		}
	}
	stage.Children(func() {
		if state.started {
			c.nextKey, c.keySet = "content", true
			coreColumn(c).Absolute().Top(0).Left(0).Fill().Opacity(1 - opacity).
				Disabled(!state.finished).Children(func() {
				// Inert also guards router shortcuts which do not rely on
				// the target element's disabled appearance.
				previous := c.inert
				c.inert = previous || !state.finished
				defer func() { c.inert = previous }()
				content()
			})
		}
		if !state.finished {
			c.nextKey, c.keySet = "splash", true
			coreColumn(c).Absolute().Top(0).Left(0).Fill().Opacity(opacity).Children(splash)
		}
	})
	return stage
}
