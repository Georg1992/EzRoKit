package autopot

// OverlayValues is the HP/SP snapshot shown on the status overlay.
// HPMax/SPMax of 100 means the HP/SP fields are percentages.
type OverlayValues struct {
	HP, HPMax, SP, SPMax           int
	PanelX, PanelY, PanelW, PanelH int
}

// StatusSink receives overlay updates from the AutoPot runner.
// Readers return data; the runner (or healer) publishes through this sink.
type StatusSink interface {
	SetMode(mode string)
	SetValues(OverlayValues)
	ClearValues()
}

func publishStatus(sink StatusSink, result BarReadResult) {
	if sink == nil {
		return
	}
	if result.Mode != "" {
		sink.SetMode(result.Mode)
	}
	if result.Values != nil {
		sink.SetValues(*result.Values)
	}
}

func setMode(sink StatusSink, mode string) {
	if sink != nil {
		sink.SetMode(mode)
	}
}

func clearPotsEndedMode(sink StatusSink, potsEnded bool) {
	if potsEnded {
		setMode(sink, "")
	}
}
