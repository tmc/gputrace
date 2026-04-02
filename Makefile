# Makefile for local gputrace development on macOS.

GPUTRACE_APP := $(HOME)/go/bin/gputrace.app
AXPERMS_APP := $(HOME)/go/bin/axperms.app
AXPERMS_BIN := $(HOME)/go/bin/axperms
BUNDLE_ID := com.tmc.gputrace
AXPERMS_BUNDLE_ID := com.github.tmc.gputrace.axperms

.PHONY: all build test vet install clean rebuild test-permissions reset-permissions axperms setup-axperms help

all: build

build:
	go install ./cmd/gputrace

test:
	go test ./...

vet:
	go vet ./...

install: clean build setup-permissions
	@echo "Reinstall complete with fresh permissions"

reinstall: build
	@# DevMode bundle reuses the stable wrapper across rebuilds, preserving TCC.
	@# Only run setup-permissions if the bundle doesn't exist yet.
	@if [ ! -d "$(GPUTRACE_APP)" ]; then \
		echo "App bundle missing, running setup-permissions..."; \
		$(MAKE) setup-permissions; \
	else \
		echo "Triggering bundle update (DevMode preserves TCC)..."; \
		gputrace xp check-status --no-prompt 2>/dev/null || true; \
	fi

# Clean app bundle to force macgo to recreate it
clean:
	rm -rf $(GPUTRACE_APP)

# Setup permissions after clean rebuild
setup-permissions:
	@echo "Step 1: Triggering bundle creation..."
	-gputrace xp check-status --no-prompt 2>/dev/null || true
	@echo "Step 2: Resetting TCC for Accessibility (clears stale code requirement)..."
	-tccutil reset Accessibility $(BUNDLE_ID) 2>/dev/null || true
	@echo "Step 3: Resetting TCC for Screen Recording..."
	-tccutil reset ScreenCapture $(BUNDLE_ID) 2>/dev/null || true
	@echo "Step 4: Re-triggering permission prompt (adds app to list with fresh signature)..."
	-gputrace xp check-status --no-prompt 2>/dev/null || true
	# -gputrace xp screenshot --no-prompt 2>/dev/null || true
	@sleep 2
	@echo "Step 5: Opening System Settings Accessibility pane..."
	$(AXPERMS_BIN) -open 2>/dev/null || true
	@sleep 2
	@echo "Step 6: Enabling accessibility permission..."
	$(AXPERMS_BIN) -enable gputrace.app 2>/dev/null | grep -v "macgo:" || true
	@sleep 2
	# @echo "Step 7: Enabling screen recording permission..."
	# $(AXPERMS_BIN) -enable-screen-recording gputrace.app 2>/dev/null | grep -v "macgo:" || true
	@echo "Step 8: Verifying permissions..."
	@gputrace xp check-status --no-prompt && echo "✓ Accessibility OK" || echo "✗ Accessibility permission may need manual intervention"
	# @gputrace xp screenshot --no-prompt -o /tmp/test-screenshot.png 2>/dev/null && echo "✓ Screen Recording OK" || echo "✗ Screen Recording permission may need manual setup in System Settings > Privacy & Security > Screen Recording"

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
