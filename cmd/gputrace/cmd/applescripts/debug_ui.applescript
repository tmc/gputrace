-- Dump Xcode window UI elements to find buttons
tell application "System Events"
	tell process "Xcode"
		set LF to ASCII character 10
		set output to "=== Xcode Window UI Elements ===" & LF & LF

		try
			tell window 1
				-- Get window info
				set output to output & "Window title: " & (name as string) & LF & LF

				-- Count total elements
				set allElements to entire contents
				set output to output & "Total UI elements: " & (count of allElements) & LF & LF

				-- Find all buttons
				set output to output & "--- All Buttons Found ---" & LF
				set buttonCount to 0
				repeat with elem in allElements
					try
						if class of elem is button then
							set buttonCount to buttonCount + 1
							set btnName to "unnamed"
							try
								set btnName to name of elem
							end try
							set btnDesc to ""
							try
								set btnDesc to description of elem
							end try
							set output to output & "  Button " & buttonCount & ": '" & btnName & "'"
							if btnDesc is not "" then
								set output to output & " (" & btnDesc & ")"
							end if
							set output to output & LF
						end if
					end try
				end repeat

				set output to output & LF & "Total buttons: " & buttonCount & LF
			end tell
		on error errMsg
			set output to output & "Error: " & errMsg & LF
		end try

		return output
	end tell
end tell
