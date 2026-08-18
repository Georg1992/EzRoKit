package runner

import (
	"encoding"
	"fmt"
	"time"

	"ezrokit/runner/internal/session"

	"github.com/Alia5/VIIPER/device/keyboard"
	"github.com/Alia5/VIIPER/device/mouse"
	"github.com/Alia5/VIIPER/viiperclient"
)

var (
	_ session.InputSession        = (*ViiperSession)(nil)
	_ session.ClickerInputSession = (*ViiperSession)(nil)
)

const inputWriteTimeout = 500 * time.Millisecond

func (s *ViiperSession) Reset() {
	_ = s.do(func() error {
		_ = keyUpLocked(s.keyStream)
		_ = mouseUpLocked(s.mouseStream)
		return nil
	})
}

func (s *ViiperSession) TapKey(vk int32, hold time.Duration) error {
	return s.do(func() error {
		if err := keyDownLocked(s.keyStream, vk); err != nil {
			_ = keyUpLocked(s.keyStream)
			return err
		}
		if hold > 0 {
			time.Sleep(hold)
		}
		return keyUpLocked(s.keyStream)
	})
}

// TapKeyWithClick is one clicker cycle: the game sees the skill key go down, a
// left click while it is down, then the key come up. Each HID write returns only
// after that device's USB host has polled the new state, and the whole cycle is
// serialized against other runners' input.
func (s *ViiperSession) TapKeyWithClick(vk int32, hold time.Duration) error {
	return s.do(func() error {
		if err := keyDownLocked(s.keyStream, vk); err != nil {
			_ = keyUpLocked(s.keyStream)
			return err
		}
		if hold > 0 {
			time.Sleep(hold)
		}
		clickErr := s.clickLocked(hold)
		keyErr := keyUpLocked(s.keyStream)
		if clickErr != nil {
			return clickErr
		}
		return keyErr
	})
}

func (s *ViiperSession) clickLocked(hold time.Duration) error {
	if err := mouseDownLocked(s.mouseStream); err != nil {
		_ = mouseUpLocked(s.mouseStream)
		return err
	}
	if hold > 0 {
		time.Sleep(hold)
	}
	return mouseUpLocked(s.mouseStream)
}

func (s *ViiperSession) do(fn func() error) error {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.closed {
		return errViiperSessionClosed
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()

	defer func() {
		_ = s.keyStream.SetWriteDeadline(time.Time{})
		_ = s.mouseStream.SetWriteDeadline(time.Time{})
		_ = s.keyStream.SetReadDeadline(time.Time{})
		_ = s.mouseStream.SetReadDeadline(time.Time{})
	}()
	return fn()
}

func writeWait(stream *viiperclient.DeviceStream, v encoding.BinaryMarshaler) error {
	deadline := time.Now().Add(inputWriteTimeout)
	if err := stream.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set input write deadline: %w", err)
	}
	if err := stream.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("set input read deadline: %w", err)
	}
	return stream.WriteBinaryWait(v)
}

func keyDownLocked(stream *viiperclient.DeviceStream, vk int32) error {
	hid, ok := VKToHID(vk)
	if !ok {
		return fmt.Errorf("unsupported trigger key %s", KeyName(vk))
	}
	press := keyboard.PressKey(hid)
	return writeWait(stream, &press)
}

func keyUpLocked(stream *viiperclient.DeviceStream) error {
	release := keyboard.Release()
	return writeWait(stream, &release)
}

func mouseDownLocked(stream *viiperclient.DeviceStream) error {
	return writeWait(stream, &mouse.InputState{Buttons: mouse.BtnLeft})
}

func mouseUpLocked(stream *viiperclient.DeviceStream) error {
	return writeWait(stream, &mouse.InputState{})
}
