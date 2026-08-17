package runner

import (
	"fmt"
	"time"

	"ezrokit/runner/internal/timing"

	"github.com/Alia5/VIIPER/device/keyboard"
	"github.com/Alia5/VIIPER/device/mouse"
	"github.com/Alia5/VIIPER/viiperclient"
)

// Reset releases all keys and mouse buttons without closing streams, removing
// devices, or removing the bus. The session stays reusable after a Stop.
func (s *ViiperSession) Reset() {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	s.keyMu.Lock()
	_ = keyUpLocked(s.keyStream)
	s.keyMu.Unlock()
	s.mouseMu.Lock()
	_ = mouseUpLocked(s.mouseStream)
	s.mouseMu.Unlock()
}

func (s *ViiperSession) TapKey(vk int32, hold time.Duration) error {
	// Keep the read lock until the action lock is acquired. Close takes
	// the write lock first, then waits for the in-flight device operation;
	// this prevents a pre-close caller from starting a write after Close
	// has already closed the stream. actionMu covers the complete
	// key-down → hold → key-up action, so other runners cannot interrupt it.
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return errViiperSessionClosed
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	return s.tapKeyLocked(vk, hold)
}

// TapKeyThenMouseClick performs the clicker's key and mouse actions as one
// serialized operation. No other runner can insert input between them.
func (s *ViiperSession) TapKeyThenMouseClick(vk int32, keyHold, mouseHold time.Duration) error {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return errViiperSessionClosed
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()

	if err := s.tapKeyLocked(vk, keyHold); err != nil {
		return err
	}
	return s.mouseClickLocked(mouseHold)
}

func (s *ViiperSession) tapKeyLocked(vk int32, hold time.Duration) error {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if err := keyDownLocked(s.keyStream, vk); err != nil {
		return err
	}
	time.Sleep(hold)
	return keyUpLocked(s.keyStream)
}

func (s *ViiperSession) MouseClick(hold time.Duration) error {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return errViiperSessionClosed
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	return s.mouseClickLocked(hold)
}

func (s *ViiperSession) mouseClickLocked(hold time.Duration) error {
	s.mouseMu.Lock()
	defer s.mouseMu.Unlock()
	return mouseClickLocked(s.mouseStream, hold)
}

// mouseClickLocked sends the button-down state twice before releasing it.
// VIIPER's mouse device coalesces pending states in a one-entry channel;
// retransmitting the identical down state ensures the pressed state spans
// multiple HID polling opportunities without creating a second click.
func mouseClickLocked(stream *viiperclient.DeviceStream, hold time.Duration) error {
	if err := mouseDownLocked(stream); err != nil {
		return err
	}

	minimumHold := 2 * timing.HIDPollInterval
	if hold < minimumHold {
		hold = minimumHold
	}
	firstHalf := hold / 2
	time.Sleep(firstHalf)

	if err := mouseDownLocked(stream); err != nil {
		_ = mouseUpLocked(stream)
		return err
	}
	time.Sleep(hold - firstHalf)
	return mouseUpLocked(stream)
}

func keyDownLocked(stream *viiperclient.DeviceStream, vk int32) error {
	hid, ok := VKToHID(vk)
	if !ok {
		return fmt.Errorf("unsupported trigger key %s", KeyName(vk))
	}
	press := keyboard.PressKey(hid)
	return stream.WriteBinary(&press)
}

func keyUpLocked(stream *viiperclient.DeviceStream) error {
	release := keyboard.Release()
	return stream.WriteBinary(&release)
}

func mouseDownLocked(stream *viiperclient.DeviceStream) error {
	return stream.WriteBinary(&mouse.InputState{Buttons: mouse.BtnLeft})
}

func mouseUpLocked(stream *viiperclient.DeviceStream) error {
	return stream.WriteBinary(&mouse.InputState{})
}
