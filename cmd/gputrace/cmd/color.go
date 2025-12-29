package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Global color mode
var colorMode string

// ANSI Colors
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorGray   = "\033[37m"
	ColorBold   = "\033[1m"
)

func initColorFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&colorMode, "color", "auto", "Colorize output: auto, always, off")
}

func shouldColor() bool {
	switch colorMode {
	case "always":
		return true
	case "off":
		return false
	case "auto":
		// Check for NO_COLOR env var
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		// Simple TTY check could go here, for now default to true in this context
		return true
	default:
		return true
	}
}

func Colorize(text string, color string) string {
	if !shouldColor() {
		return text
	}
	return color + text + ColorReset
}

func CPrintf(color string, format string, a ...interface{}) {
	fmt.Print(Colorize(fmt.Sprintf(format, a...), color))
}

func CPrintln(color string, a ...interface{}) {
	fmt.Println(Colorize(fmt.Sprint(a...), color))
}
