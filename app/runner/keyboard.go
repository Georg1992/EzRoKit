package runner

// PhysicalKeyDown is GetAsyncKeyState for the bind. The clicker taps the
// virtual key every cycle so this can see a real release.
var PhysicalKeyDown = func(vk int32) bool { return false }

// EmergencyKeyDown is GetAsyncKeyState for End/F12.
var EmergencyKeyDown = func(vk int32) bool { return false }

// PollKeyToggle detects a rising edge on vk.
var PollKeyToggle = func(wasDown *bool, vk int32) bool { return false }
