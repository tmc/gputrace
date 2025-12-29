-- Export GPU trace with performance data
tell application "Xcode"
	activate
	delay 0.3
end tell

tell application "System Events"
	tell process "Xcode"
		set frontmost to true
		delay 0.3

		-- Try various Export menu item names with correct syntax
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
			try
				click menu item "Export" of menu 1 of menu bar item "File" of menu bar 1
				set exportClicked to true
			end try
		end if

		if not exportClicked then
			try
				-- List all File menu items for debugging
				set menuItems to name of every menu item of menu 1 of menu bar item "File" of menu bar 1
				error "Export not found. File menu items: " & (menuItems as string)
			on error e
				error "Export menu item not found: " & e
			end try
		end if

		-- Wait for save/export sheet
		delay 1.5
		repeat with i from 1 to 30
			try
				if exists sheet 1 of window 1 then
					exit repeat
				end if
			end try
			delay 0.5
		end repeat

		-- Check if sheet appeared
		try
			if not (exists sheet 1 of window 1) then
				error "Export sheet did not appear"
			end if
		end try

		delay 0.5

		-- Navigate to output directory
		keystroke "g" using {command down, shift down}
		delay 1.0

		keystroke "{{OUTPUT_DIR}}"
		delay 0.5
		keystroke return
		delay 1.0

		-- Type filename
		keystroke "a" using {command down}
		delay 0.3
		keystroke "{{OUTPUT_NAME}}"
		delay 0.5

		-- Try to check "Embed performance data" checkbox
		try
			tell sheet 1 of window 1
				set embedCheckbox to checkbox "Embed performance data"
				if value of embedCheckbox is 0 then
					click embedCheckbox
				end if
			end tell
		end try

		-- Save
		keystroke return
		delay 1.0

		return "Export initiated"
	end tell
end tell
