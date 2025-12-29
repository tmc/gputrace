-- Trigger replay in Xcode GPU trace window using keyboard shortcut
tell application "Xcode"
	activate
	delay 0.5
end tell

tell application "System Events"
	tell process "Xcode"
		set frontmost to true
		delay 0.3

		-- Use Ctrl+R keyboard shortcut (works for GPU trace replay)
		keystroke "r" using {control down}
		delay 0.3
		return "Sent Ctrl+R to trigger replay"
	end tell
end tell
