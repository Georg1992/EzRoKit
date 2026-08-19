//go:build windows

package main

import (
	"ezrokit/runner"

	"github.com/lxn/walk"
)

// populateWindowComboBox fills a walk ComboBox with visible window titles.
// Returns the full window list so the caller can map selection to PID.
func populateWindowComboBox(cb *walk.ComboBox) ([]runner.VisibleWindow, error) {
	items := runner.ListVisibleWindows()

	if len(items) == 0 {
		// No windows found — this is unusual on a running Windows desktop
		// and may indicate that the EnumWindows call failed or all windows
		// have empty titles (e.g. during a fast user switch / lock screen).
		// Leave the old model intact so the user doesn't lose their selection.
		return items, nil
	}

	selIdx := cb.CurrentIndex()
	selTitle := ""
	if selIdx >= 0 {
		selTitle = cb.Text()
	}

	cb.SetModel(nil)
	titles := make([]string, 0, len(items))
	for _, w := range items {
		titles = append(titles, w.Title)
	}
	if err := cb.SetModel(titles); err != nil {
		return nil, err
	}

	if selTitle != "" {
		for i, t := range titles {
			if t == selTitle {
				cb.SetCurrentIndex(i)
				break
			}
		}
	}

	return items, nil
}
