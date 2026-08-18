package runner

// PhysicalKeyDown reports whether the user is holding vk on a real keyboard.
// It comes from Raw Input, per device, so VIIPER typing the same key while the
// clicker runs cannot look like the user pressing or releasing the bind.
var PhysicalKeyDown = func(vk int32) bool { return false }

// SetKeyboardLog receives the hold layer's keyboard lines: the keyboards found at
// startup, and anything plugged in or unplugged later.
var SetKeyboardLog = func(func(string)) {}

// EmergencyKeyDown is GetAsyncKeyState for End/F12.
var EmergencyKeyDown = func(vk int32) bool { return false }

// PollKeyToggle detects a rising edge on vk.
var PollKeyToggle = func(wasDown *bool, vk int32) bool { return false }
