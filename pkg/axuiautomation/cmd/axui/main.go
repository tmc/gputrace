// Command axui is a CLI for macOS accessibility UI automation.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/pkg/axuiautomation"
	"github.com/tmc/macgo"
)

var jsonOutput bool
var limit int

func main() {
	runtime.LockOSThread()
	macgo.Start(&macgo.Config{
		Permissions: []macgo.Permission{macgo.Accessibility},
		//DevMode: true,
	})

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "axui",
	Short: "macOS accessibility UI automation CLI",
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	rootCmd.PersistentFlags().IntVar(&limit, "limit", 500, "Element search limit")

	rootCmd.AddCommand(trustedCmd, appCmd, windowsCmd, findCmd, clickCmd, treeCmd, moveCmd)
}

var trustedCmd = &cobra.Command{
	Use:   "trusted",
	Short: "Check accessibility permissions",
	Run: func(cmd *cobra.Command, args []string) {
		t := axuiautomation.IsProcessTrusted()
		if jsonOutput {
			json.NewEncoder(os.Stdout).Encode(map[string]bool{"trusted": t})
		} else {
			fmt.Printf("Trusted: %v\n", t)
		}
	},
}

var appCmd = &cobra.Command{
	Use:   "app <bundle-id|pid>",
	Short: "Show app info",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := getApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		root := app.Root()
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"pid": app.PID(), "running": app.IsRunning(),
				"title": root.Title(), "role": root.Role(),
			})
		}
		fmt.Printf("PID: %d\nRunning: %v\nTitle: %s\nRole: %s\n",
			app.PID(), app.IsRunning(), root.Title(), root.Role())
		return nil
	},
}

var windowsCmd = &cobra.Command{
	Use:   "windows <bundle-id|pid>",
	Short: "List windows",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := getApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		windows := app.Windows().AllElements()
		if jsonOutput {
			var out []map[string]any
			for _, w := range windows {
				f := w.Frame()
				out = append(out, map[string]any{
					"title": w.Title(), "role": w.Role(), "doc": w.Document(),
					"x": f.Origin.X, "y": f.Origin.Y, "w": f.Size.Width, "h": f.Size.Height,
				})
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		}
		for i, w := range windows {
			f := w.Frame()
			fmt.Printf("[%d] %q @ (%.0f,%.0f) %.0fx%.0f\n",
				i, w.Title(), f.Origin.X, f.Origin.Y, f.Size.Width, f.Size.Height)
		}
		return nil
	},
}

var findCmd = &cobra.Command{
	Use:   "find <bundle-id|pid>",
	Short: "Find elements",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := getApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		role, _ := cmd.Flags().GetString("role")
		title, _ := cmd.Flags().GetString("title")
		contains, _ := cmd.Flags().GetString("contains")

		q := app.Descendants().WithLimit(limit)
		if role != "" {
			q = q.ByRole(role)
		}
		if title != "" {
			q = q.ByTitle(title)
		}
		if contains != "" {
			q = q.ByTitleContains(contains)
		}

		elements := q.AllElements()
		if jsonOutput {
			var out []map[string]any
			for _, el := range elements {
				x, y := el.Position()
				w, h := el.Size()
				out = append(out, map[string]any{
					"role": el.Role(), "title": el.Title(), "desc": el.Description(),
					"enabled": el.IsEnabled(), "value": el.Value(),
					"x": x, "y": y, "w": w, "h": h,
				})
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		}
		fmt.Printf("Found %d elements:\n", len(elements))
		for i, el := range elements {
			t := el.Title()
			if t == "" {
				t = el.Description()
			}
			fmt.Printf("[%d] %s %q enabled=%v\n", i, el.Role(), t, el.IsEnabled())
		}
		return nil
	},
}

func init() {
	findCmd.Flags().StringP("role", "r", "", "Filter by role")
	findCmd.Flags().StringP("title", "t", "", "Filter by title")
	findCmd.Flags().StringP("contains", "c", "", "Filter by title substring")
}

var clickCmd = &cobra.Command{
	Use:   "click <bundle-id|pid> <title>",
	Short: "Click element by title",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := getApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		el := app.Descendants().WithLimit(limit).ByTitleContains(args[1]).First()
		if el == nil {
			return fmt.Errorf("element %q not found", args[1])
		}
		fmt.Printf("Clicking: %s %q\n", el.Role(), el.Title())
		return el.Click()
	},
}

var treeCmd = &cobra.Command{
	Use:   "tree <bundle-id|pid>",
	Short: "Show element tree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := getApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		depth, _ := cmd.Flags().GetInt("depth")
		printTree(app.Root(), 0, depth)
		return nil
	},
}

func init() {
	treeCmd.Flags().IntP("depth", "d", 3, "Max depth")
}

func printTree(el *axuiautomation.Element, indent, max int) {
	if el == nil || indent > max {
		return
	}
	prefix := strings.Repeat("  ", indent)
	role := el.Role()
	title := el.Title()
	desc := el.Description()
	value := el.Value()

	// Build info string
	var info []string
	if title != "" {
		info = append(info, fmt.Sprintf("title=%q", title))
	}
	if desc != "" {
		info = append(info, fmt.Sprintf("desc=%q", desc))
	}
	if value != "" && value != "0" {
		info = append(info, fmt.Sprintf("value=%q", value))
	}

	if len(info) > 0 {
		fmt.Printf("%s%s [%s]\n", prefix, role, strings.Join(info, " "))
	} else {
		fmt.Printf("%s%s\n", prefix, role)
	}
	for _, c := range el.Children() {
		printTree(c, indent+1, max)
	}
}

var moveCmd = &cobra.Command{
	Use:   "move <bundle-id|pid> <x> <y>",
	Short: "Move first window to position",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := getApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		x, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			return fmt.Errorf("invalid x: %v", err)
		}
		y, err := strconv.ParseFloat(args[2], 64)
		if err != nil {
			return fmt.Errorf("invalid y: %v", err)
		}

		windows := app.Windows().AllElements()
		if len(windows) == 0 {
			return fmt.Errorf("no windows found")
		}

		if err := windows[0].SetPosition(x, y); err != nil {
			return err
		}
		fmt.Printf("Moved window to (%.0f, %.0f)\n", x, y)
		return nil
	},
}

func getApp(arg string) (*axuiautomation.Application, error) {
	if pid, err := strconv.ParseInt(arg, 10, 32); err == nil {
		app := axuiautomation.NewApplicationFromPID(int32(pid))
		if app == nil {
			return nil, fmt.Errorf("cannot connect to PID %d", pid)
		}
		return app, nil
	}
	return axuiautomation.NewApplication(arg)
}
