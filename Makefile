# Makefile for local gputrace development on macOS.

GPUTRACE_APP := $(HOME)/go/bin/gputrace.app
AXPERMS_APP := $(HOME)/go/bin/axperms.app
AXPERMS_BIN := $(HOME)/go/bin/axperms
BUNDLE_ID := com.tmc.gputrace
AXPERMS_BUNDLE_ID := com.github.tmc.gputrace.axperms

.PHONY: all build test vet install clean rebuild test-permissions reset-permissions axperms setup-axperms sign-bundle help

all: build

build:
	go install ./cmd/gputrace

test:
	go test ./...

vet:
	go vet ./...

install: clean build setup-permissions
	@echo "Reinstall complete with fresh permissions"

reinstall: build sign-bundle
	@# DevMode bundle reuses the stable wrapper across rebuilds, preserving TCC.
	@# sign-bundle ensures the bundle is properly signed after go install.
	@# Only run full setup-permissions if the bundle was just created.
	@gputrace xp check-status --no-prompt 2>/dev/null && echo "✓ Accessibility OK" || \
		echo "⚠ Accessibility permission not granted — run 'make setup-permissions' or approve in System Settings"

# Clean app bundle to force macgo to recreate it
clean:
	rm -rf $(GPUTRACE_APP)

# Sign the app bundle with best available identity.
# Works around macgo v0.1.0 bug where .dev_target in Contents/ breaks codesign
# (codesign treats non-Mach-O files in Contents/ as unsigned subcomponents).
sign-bundle:
	@if [ ! -d "$(GPUTRACE_APP)" ]; then \
		echo "No app bundle found, triggering creation..."; \
		gputrace xp check-status --no-prompt 2>/dev/null || true; \
	fi
	@# Temporarily move .dev_target out of Contents/ during signing
	@# (macgo v0.1.0 needs it in Contents/ for DevMode detection)
	@if [ -f "$(GPUTRACE_APP)/Contents/.dev_target" ]; then \
		cp "$(GPUTRACE_APP)/Contents/.dev_target" /tmp/.dev_target.bak; \
		rm "$(GPUTRACE_APP)/Contents/.dev_target"; \
	fi
	@# Sign with best available identity, fall back to ad-hoc
	@IDENTITY=$$(security find-identity -v -p codesigning 2>/dev/null | head -1 | sed 's/.*"\(.*\)"/\1/'); \
	if [ -n "$$IDENTITY" ]; then \
		echo "Signing bundle with: $$IDENTITY"; \
		codesign --force --sign "$$IDENTITY" --identifier $(BUNDLE_ID) --timestamp "$(GPUTRACE_APP)"; \
	else \
		echo "No signing identity found, using ad-hoc"; \
		codesign --force --sign - --identifier $(BUNDLE_ID) "$(GPUTRACE_APP)"; \
	fi
	@# Restore .dev_target for macgo DevMode bundle reuse detection
	@if [ -f /tmp/.dev_target.bak ]; then \
		mv /tmp/.dev_target.bak "$(GPUTRACE_APP)/Contents/.dev_target"; \
	fi

# Setup permissions after clean rebuild
setup-permissions: sign-bundle
	@echo "Step 1: Resetting TCC for Accessibility (clears stale code requirement)..."
	-tccutil reset Accessibility $(BUNDLE_ID) 2>/dev/null || true
	@echo "Step 2: Resetting TCC for Screen Recording..."
	-tccutil reset ScreenCapture $(BUNDLE_ID) 2>/dev/null || true
	@echo "Step 3: Re-triggering permission prompt (adds app to list with fresh signature)..."
	-gputrace xp check-status --no-prompt 2>/dev/null || true
	@sleep 2
	@echo "Step 4: Opening System Settings Accessibility pane..."
	$(AXPERMS_BIN) -open 2>/dev/null || true
	@sleep 2
	@echo "Step 5: Enabling accessibility permission..."
	$(AXPERMS_BIN) -enable gputrace.app 2>/dev/null | grep -v "macgo:" || true
	@sleep 2
	@echo "Step 6: Verifying permissions..."
	@gputrace xp check-status --no-prompt && echo "✓ Accessibility OK" || echo "✗ Accessibility permission may need manual intervention"

# Full permission reset (use when TCC database is stale)
reset-permissions:
	@echo "Resetting TCC entries..."
	tccutil reset Accessibility $(BUNDLE_ID) 2>/dev/null || true
	tccutil reset ScreenCapture $(BUNDLE_ID) 2>/dev/null || true
	tccutil reset Accessibility $(AXPERMS_BUNDLE_ID) 2>/dev/null || true
	@echo "Re-triggering permission prompts..."
	-$(AXPERMS_BIN) -prompt 2>&1 | grep -v "macgo:" || true
	-gputrace xp check-status --no-prompt 2>/dev/null || true
	-gputrace xp screenshot --no-prompt -o /tmp/test-screenshot.png 2>/dev/null || true
	@echo ""
	@echo "Please manually enable axperms and gputrace in System Settings,"
	@echo "then run 'make setup-permissions'"

fullreinstall: clean build setup-permissions
	@echo "Full reinstall complete (bundle recreated, permissions reset)"

reset: clean build setup-permissions

# Quick test that permissions work
test-permissions:
	gputrace xp check-status --no-prompt

# Build axperms helper and update bundle
axperms:
	go build -o $(AXPERMS_BIN) ./cmd/axperms
	@# Update the binary inside the app bundle if it exists
	@if [ -d "$(AXPERMS_APP)/Contents/MacOS" ]; then \
		cp $(AXPERMS_BIN) $(AXPERMS_APP)/Contents/MacOS/axperms; \
	fi

# First-time setup for axperms - requires manual user action
# Run this ONCE before using axperms to manage permissions
setup-axperms: axperms
	@echo "Setting up axperms Accessibility permission..."
	@echo "This is a ONE-TIME setup - axperms needs Accessibility permission"
	@echo "to manipulate System Settings UI for other apps."
	@echo ""
	@echo "Resetting any stale axperms TCC entry..."
	-tccutil reset Accessibility $(AXPERMS_BUNDLE_ID) 2>/dev/null || true
	@echo ""
	@echo "Triggering permission prompt..."
	@# Run axperms to trigger the prompt - it will fail but add itself to the list
	-$(AXPERMS_BIN) -prompt 2>&1 | grep -v "macgo:" || true
	@echo ""
	@echo "============================================"
	@echo "ACTION REQUIRED:"
	@echo "1. System Settings should now be open to Privacy & Security > Accessibility"
	@echo "2. Find 'axperms' in the list"
	@echo "3. Toggle it ON"
	@echo "4. You may need to authenticate with your password"
	@echo "5. Then run 'make setup-permissions' to configure gputrace"
	@echo "============================================"

help:
	@echo "gputrace Makefile"
	@echo ""
	@echo "Development targets:"
	@echo "  build              - Build gputrace"
	@echo "  test               - Run Go tests"
	@echo "  vet                - Run go vet"
	@echo "  reinstall          - Rebuild binary (preserves TCC permissions via DevMode)"
	@echo "  fullreinstall      - Clean + rebuild + fresh permissions (resets TCC)"
	@echo "  clean              - Remove app bundle (forces macgo to recreate)"
	@echo ""
	@echo "Permission setup (run in order for first-time setup):"
	@echo "  setup-axperms      - ONE-TIME: Grant axperms Accessibility (manual step)"
	@echo "  setup-permissions  - Setup gputrace Accessibility + Screen Recording"
	@echo "  reset-permissions  - Full TCC reset + setup (for stale permissions)"
	@echo "  test-permissions   - Quick test that permissions work"
	@echo ""
	@echo "Helper tools:"
	@echo "  axperms            - Build axperms helper tool"
