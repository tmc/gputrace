-- Check if GPU trace replay/profiling is complete
-- Returns "complete" when done, anything else means still in progress
tell application "System Events"
	tell process "Xcode"
		-- Check window title for indicators
		try
			set winTitle to name of window 1
			-- If window title contains "Profiled" it's done
			if winTitle contains "Profiled" then
				return "complete"
			end if
		end try

		-- Look for progress indicators (if none visible, likely complete)
		set progressFound to false
		try
			set allElements to entire contents of window 1
			repeat with elem in allElements
				try
					set elemClass to class of elem as string
					-- Check for progress indicators
					if elemClass contains "progress" then
						set progressFound to true
						exit repeat
					end if
					-- Check for busy indicators
					if elemClass contains "busy" then
						set progressFound to true
						exit repeat
					end if
				end try
			end repeat
		end try

		-- If we found progress indicators, still running
		if progressFound then
			return "in_progress"
		end if

		-- Check for "Show Performance" or "Export" buttons as completion indicators
		try
			set buttonNames to name of every button of window 1
			repeat with btnName in buttonNames
				if btnName is "Show Performance" or btnName is "Export" then
					return "complete"
				end if
			end repeat
		end try

		-- Check in toolbar
		try
			set toolbarButtons to name of every button of toolbar 1 of window 1
			repeat with btnName in toolbarButtons
				if btnName is "Export" then
					return "complete"
				end if
			end repeat
		end try

		-- Default: assume still in progress
		return "in_progress"
	end tell
end tell
