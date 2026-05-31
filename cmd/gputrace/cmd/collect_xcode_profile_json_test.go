package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRejectUnsupportedXcodeProfileJSON(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})

	collectProfileJSON = false
	if err := rejectUnsupportedXcodeProfileJSON("list-menus"); err != nil {
		t.Fatalf("rejectUnsupportedXcodeProfileJSON without JSON = %v, want nil", err)
	}

	collectProfileJSON = true
	err := rejectUnsupportedXcodeProfileJSON("list-menus")
	if err == nil {
		t.Fatal("rejectUnsupportedXcodeProfileJSON with JSON returned nil")
	}
	if got, want := err.Error(), "list-menus does not support --json"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if got := err.Error(); got != strings.ToLower(got) {
		t.Fatalf("error = %q, want lowercase", got)
	}
}

func TestUnsupportedXcodeProfileJSONArgsRejectsBeforeValidation(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})

	collectProfileJSON = true
	called := false
	validate := func(cmd *cobra.Command, args []string) error {
		called = true
		return nil
	}

	err := unsupportedXcodeProfileJSONArgs("click-menu", validate)(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("unsupportedXcodeProfileJSONArgs returned nil")
	}
	if called {
		t.Fatal("validator called after JSON rejection")
	}
}

func TestUnsupportedXcodeProfileJSONArgsPreservesValidation(t *testing.T) {
	oldJSON := collectProfileJSON
	t.Cleanup(func() {
		collectProfileJSON = oldJSON
	})

	collectProfileJSON = false
	validate := unsupportedXcodeProfileJSONArgs("ensure-checked", cobra.ExactArgs(1))

	if err := validate(&cobra.Command{}, []string{"Profile after replay"}); err != nil {
		t.Fatalf("valid args returned error: %v", err)
	}
	if err := validate(&cobra.Command{}, nil); err == nil {
		t.Fatal("missing args returned nil error")
	}
}
