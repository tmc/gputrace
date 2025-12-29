-- Export GPU trace with performance data
tell application "Xcode"
	activate
	delay 0.5
end tell

tell application "System Events"
	tell process "Xcode"
		set frontmost to true
		delay 0.5

		-- Try to click Export menu item
		set exportClicked to false

		try
			click menu item "Export…" of menu 1 of menu bar item "File" of menu bar 1
			set exportClicked to true
		end try

		if not exportClicked then
			try
				click menu item "Export..." of menu 1 of menu bar item "File" of menu bar 1
				set exportClicked to true
			end try
		end if

		if not exportClicked then
			-- Try keyboard shortcut Cmd+Shift+E (common export shortcut)
			try
				keystroke "e" using {command down, shift down}
				set exportClicked to true
			end try
		end if

		if not exportClicked then
			error "Could not find Export menu item"
		end if

		-- Wait for export sheet to appear
		delay 2
		set sheetFound to false
		repeat with i from 1 to 20
			try
				if exists sheet 1 of window 1 then
					set sheetFound to true
					exit repeat
				end if
			end try
			delay 0.5
		end repeat

		if not sheetFound then
			error "Export sheet did not appear"
		end if

		delay 1

		-- Navigate to output directory using Go To Folder
		keystroke "g" using {command down, shift down}
		delay 1.5

		-- Type the output directory
		keystroke "{{OUTPUT_DIR}}"
		delay 0.5
		keystroke return
		delay 2

		-- Select filename field and type new name
		keystroke "a" using {command down}
		delay 0.3
		keystroke "{{OUTPUT_NAME}}"
		delay 0.5

		-- Look for and check "Embed performance data" checkbox if it exists
		try
			tell sheet 1 of window 1
				set checkboxes to every checkbox
				repeat with cb in checkboxes
					try
						if name of cb contains "performance" or name of cb contains "Embed" then
							if value of cb is 0 then
								click cb
							end if
						end if
					end try
				end repeat
			end tell
		end try

		delay 0.5

		-- Click Save button
		try
			click button "Save" of sheet 1 of window 1
		on error
			-- Try pressing Return as fallback
			keystroke return
		end try

		delay 2
		return "Export initiated"
	end tell
end tell
