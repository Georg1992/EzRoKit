package runner

// PhysicalKeyDown returns true if the virtual key vk is currently held down.
// Defaults to a no-op; the real implementation is wired via init() in
// keyboard_windows.go (or per-platform equivalents).
var PhysicalKeyDown = func(vk int32) bool { return false }

// EmergencyKeyDown reports the desktop state of an emergency stop key such
// as End or F12. It is separate from PhysicalKeyDown because trigger state
// must remain isolated from virtual VIIPER input.
var EmergencyKeyDown = func(vk int32) bool { return false }

// PollKeyToggle detects a rising edge on vk: returns true on the first
// poll where vk transitions from released to pressed. wasDown must be
// a caller-owned bool tracking the previous state.
var PollKeyToggle = func(wasDown *bool, vk int32) bool { return false }
