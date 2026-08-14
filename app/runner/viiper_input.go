package runner

import (
	"fmt"
	"time"

	"belarus-champ-tools/runner/internal/timing"

	"github.com/Alia5/VIIPER/device/keyboard"
	"github.com/Alia5/VIIPER/device/mouse"
	"github.com/Alia5/VIIPER/viiperclient"
)

// Reset releases all keys and mouse buttons without closing streams, removing
// devices, or removing the bus. The session stays reusable after a Stop.
func (s *ViiperSession) Reset() {
	s.writeMu.Lock()
	_ = keyUpLocked(s.keyStream)
	_ = mouseUpLocked(s.mouseStream)
	s.writeMu.Unlock()
}

func (s *ViiperSession) TapKey(vk int32, hold time.Duration) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := keyDownLocked(s.keyStream, vk); err != nil {
		return err
	}
	time.Sleep(hold)
	return keyUpLocked(s.keyStream)
}

// ClickerCycle emits key click -> optional mouse click while holding the wire
// lock for both actions. This prevents another runner from inserting an input
// event between the clicker's required key and mouse portions.
func (s *ViiperSession) ClickerCycle(vk int32, keyHold, mouseHold time.Duration) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := keyDownLocked(s.keyStream, vk); err != nil {
		return err
	}
	if err := keyUpAfterLocked(s.keyStream, keyHold); err != nil {
		return err
	}
	return mouseClickLocked(s.mouseStream, mouseHold)
}

func (s *ViiperSession) MouseClick(hold time.Duration) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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

func keyUpAfterLocked(stream *viiperclient.DeviceStream, hold time.Duration) error {
	time.Sleep(hold)
	return keyUpLocked(stream)
}

func mouseDownLocked(stream *viiperclient.DeviceStream) error {
	return stream.WriteBinary(&mouse.InputState{Buttons: mouse.BtnLeft})
}

func mouseUpLocked(stream *viiperclient.DeviceStream) error {
	return stream.WriteBinary(&mouse.InputState{})
}
