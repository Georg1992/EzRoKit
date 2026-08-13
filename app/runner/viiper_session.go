package runner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"belarus-champ-tools/runner/internal/timing"

	"github.com/Alia5/VIIPER/device/keyboard"
	"github.com/Alia5/VIIPER/device/mouse"
	"github.com/Alia5/VIIPER/viiperclient"
)

// ViiperSession owns one VIIPER bus with a shared keyboard and mouse.
// All runners use the same devices; writes are serialized.
type ViiperSession struct {
	api        *viiperclient.Client
	busID      uint32
	createdBus bool

	writeMu     sync.Mutex
	keyStream   *viiperclient.DeviceStream
	mouseStream *viiperclient.DeviceStream

	closeOnce sync.Once
}

// OpenViiperSession creates a VIIPER session with keyboard and mouse
// virtual devices. The log callback receives device-ready messages
// from the calling goroutine — marshalling to the GUI thread is the
// caller's responsibility.
//
// When reusing an existing bus, all devices on it are removed first
// to guarantee fresh usbip-win2 auto-attach. This prevents stale
// HID device nodes from previous runs (e.g. after a crash) from
// blocking input to an already-running game.
func OpenViiperSession(ctx context.Context, apiAddr string, log func(string)) (*ViiperSession, error) {
	if apiAddr == "" {
		apiAddr = DefaultAPIAddr
	}
	if log == nil {
		log = noopLog
	}

	api := viiperclient.New(apiAddr)
	if _, err := api.PingCtx(ctx); err != nil {
		return nil, fmt.Errorf("viiper ping: %w", err)
	}

	busID, createdBus, err := ensureBus(ctx, api, log)
	if err != nil {
		return nil, err
	}

	// When reusing an existing bus, remove all devices so the new
	// keyboard/mouse get fresh auto-attach. Stale devices from a
	// previous run (e.g. after a crash) would otherwise stay attached
	// to usbip-win2 and the game would keep reading from dead nodes.
	if !createdBus {
		cleanBusDevices(ctx, api, busID, log)
	}

	keyStream, _, err := api.AddDeviceAndConnect(ctx, busID, "keyboard", nil)
	if err != nil {
		if createdBus {
			cleanupBus(ctx, api, busID, true, log)
		}
		return nil, fmt.Errorf("keyboard: %w", err)
	}
	log("Virtual keyboard ready")

	mouseStream, _, err := api.AddDeviceAndConnect(ctx, busID, "mouse", nil)
	if err != nil {
		_ = keyStream.Close()
		cleanupDevice(ctx, api, keyStream.BusID, keyStream.DevID, log)
		if createdBus {
			cleanupBus(ctx, api, busID, true, log)
		}
		return nil, fmt.Errorf("mouse: %w", err)
	}
	log("Virtual mouse ready")

	return &ViiperSession{
		api:         api,
		busID:       busID,
		createdBus:  createdBus,
		keyStream:   keyStream,
		mouseStream: mouseStream,
	}, nil
}

// Reset releases all keys / mouse buttons without closing streams,
// removing devices, or removing the bus. The session stays alive and
// can be reused by a subsequent Start. Call Close() for full cleanup
// when the application exits.
func (s *ViiperSession) Reset() {
	s.writeMu.Lock()
	_ = keyUpLocked(s.keyStream)
	_ = mouseUpLocked(s.mouseStream)
	s.writeMu.Unlock()
}

func (s *ViiperSession) Close() {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), timing.SessionCloseWait)
		defer cancel()

		s.writeMu.Lock()
		_ = keyUpLocked(s.keyStream)
		_ = mouseUpLocked(s.mouseStream)
		s.writeMu.Unlock()

		_ = s.keyStream.Close()
		_ = s.mouseStream.Close()
		cleanupDevice(ctx, s.api, s.keyStream.BusID, s.keyStream.DevID, noopLog)
		cleanupDevice(ctx, s.api, s.mouseStream.BusID, s.mouseStream.DevID, noopLog)
		cleanupBus(ctx, s.api, s.busID, s.createdBus, noopLog)
	})
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

// ClickerCycle emits key click -> mouse click while holding the wire lock for
// both actions. This prevents another runner from inserting an input event
// between the key and mouse portions of the clicker's required flow.
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
// VIIPER's mouse device intentionally coalesces pending states in a one-entry
// channel; without the second down report, a down followed quickly by up can
// be replaced before usbip-win2 polls the endpoint. Repeating the identical
// down state does not create a second click, but guarantees the pressed state
// spans at least two HID polling opportunities.
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
		// Best effort release if the retransmission fails, avoiding a stuck
		// virtual button while still reporting the original write error.
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

var noopLog = func(string) {}

func ensureBus(ctx context.Context, api *viiperclient.Client, log func(string)) (uint32, bool, error) {
	busesResp, err := api.BusListCtx(ctx)
	if err != nil {
		return 0, false, err
	}

	if len(busesResp.Buses) > 0 {
		busID := busesResp.Buses[0]
		for _, b := range busesResp.Buses[1:] {
			if b < busID {
				busID = b
			}
		}
		log(fmt.Sprintf("reusing VIIPER bus %d", busID))
		return busID, false, nil
	}

	resp, err := api.BusCreateCtx(ctx, 0)
	if err != nil {
		return 0, false, err
	}
	log(fmt.Sprintf("created VIIPER bus %d", resp.BusID))
	return resp.BusID, true, nil
}

// cleanBusDevices removes all devices from an existing bus so new
// devices get fresh usbip-win2 auto-attach. This prevents stale HID
// device nodes from blocking input to an already-running game.
func cleanBusDevices(ctx context.Context, api *viiperclient.Client, busID uint32, log func(string)) {
	devsResp, err := api.DevicesListCtx(ctx, busID)
	if err != nil {
		log(fmt.Sprintf("bus %d device list failed: %v", busID, err))
		return
	}
	for _, dev := range devsResp.Devices {
		if _, err := api.DeviceRemoveCtx(ctx, busID, dev.DevID); err != nil {
			log(fmt.Sprintf("bus %d device %s remove failed: %v", busID, dev.DevID, err))
		} else {
			log(fmt.Sprintf("removed stale device %d-%s", busID, dev.DevID))
		}
	}
}

func cleanupDevice(ctx context.Context, api *viiperclient.Client, busID uint32, devID string, log func(string)) {
	if _, err := api.DeviceRemoveCtx(ctx, busID, devID); err != nil {
		log(fmt.Sprintf("device remove %d-%s failed: %v", busID, devID, err))
	}
}

func cleanupBus(ctx context.Context, api *viiperclient.Client, busID uint32, created bool, log func(string)) {
	if !created {
		return
	}
	if _, err := api.BusRemoveCtx(ctx, busID); err != nil {
		log(fmt.Sprintf("bus remove %d failed: %v", busID, err))
		return
	}
	log(fmt.Sprintf("removed bus %d", busID))
}
