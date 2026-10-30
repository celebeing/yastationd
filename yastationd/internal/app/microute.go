package app

// setupMicRoute selects a board-specific capture route before any mic subscriber can open the
// device. The saved value is restored during orderly daemon shutdown after mic capture stops.
func (a *App) setupMicRoute() func() {
	mic := a.prof.Mic
	if mic.RouteControl == "" {
		return func() {}
	}

	restore, err := a.mixer.SetRestoring(mic.RouteControl, mic.RouteValue)
	if err != nil {
		a.log.Warn("mic route setup failed", "control", mic.RouteControl, "value", mic.RouteValue, "err", err)
		return func() {}
	}
	a.log.Info("mic route configured", "control", mic.RouteControl, "value", mic.RouteValue)
	return func() {
		if err := restore(); err != nil {
			a.log.Warn("mic route restore failed", "control", mic.RouteControl, "err", err)
		}
	}
}
