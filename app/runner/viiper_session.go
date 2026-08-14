package runner

import (
	"context"
	"fmt"
	"sync"

	"belarus-champ-tools/runner/internal/timing"

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
