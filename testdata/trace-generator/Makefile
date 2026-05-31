.PHONY: build run run-capture capture-all clean test help list

TRACES_DIR := ../traces
TIMESTAMP := $(shell date +%Y%m%d-%H%M%S)
CAPTURE_DIR ?= $(TRACES_DIR)/generated-$(TIMESTAMP)
RUNS ?= 3

SCENARIOS := 01-single-encoder 02-two-encoders 03-three-encoders 04-four-encoders 06-six-encoders known-invocations-1000 known-invocations-10000 low-alu-simple-add high-alu-complex-math low-occupancy-high-registers high-occupancy-low-registers

help:
	@echo "GPU Trace Generator"
	@echo "==================="
	@echo ""
	@echo "Targets:"
	@echo "  build                   Build release binary"
	@echo "  run SCENARIO=...        Run scenario without capture"
	@echo "  run-capture SCENARIO=.. Capture trace with timestamp"
	@echo "  capture-all             Capture all scenarios RUNS times into CAPTURE_DIR"
	@echo "  test                    Test build and run single-encoder"
	@echo "  clean                   Clean build artifacts"
	@echo "  list                    List available scenarios"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make run SCENARIO=01-single-encoder"
	@echo "  make run-capture SCENARIO=01-single-encoder"
	@echo "  make capture-all"
	@echo "  make test"

build:
	swift build -c release

run: build
	@if [ -z "$(SCENARIO)" ]; then \
		echo "Error: SCENARIO not specified"; \
		echo "Usage: make run SCENARIO=01-single-encoder"; \
		echo "Run 'make list' to see available scenarios"; \
		exit 1; \
	fi
	.build/release/trace-generator "$(SCENARIO)"

run-capture: build
	@if [ -z "$(SCENARIO)" ]; then \
		echo "Error: SCENARIO not specified"; \
		echo "Usage: make run-capture SCENARIO=01-single-encoder"; \
		echo "Run 'make list' to see available scenarios"; \
		exit 1; \
	fi
	@mkdir -p "$(TRACES_DIR)/$(SCENARIO)"
	@OUTPUT_PATH="$(TRACES_DIR)/$(SCENARIO)/$(SCENARIO)-$(TIMESTAMP).gputrace"; \
	echo "Capturing trace: $$OUTPUT_PATH"; \
	MTL_CAPTURE_ENABLED=1 .build/release/trace-generator "$(SCENARIO)" "$$OUTPUT_PATH"; \
	if [ -d "$$OUTPUT_PATH" ]; then \
		echo ""; \
		echo "✅ Trace captured successfully"; \
		echo "   Path: $$OUTPUT_PATH"; \
		echo "   Size: $$(du -sh "$$OUTPUT_PATH" | awk '{print $$1}')"; \
		echo ""; \
		echo "Analysis:"; \
		echo "  # Count counter files"; \
		echo "  ls $$OUTPUT_PATH/*.gpuprofiler_raw/Counters_f_*.raw 2>/dev/null | wc -l"; \
		echo ""; \
		echo "  # Open in Instruments"; \
		echo "  open $$OUTPUT_PATH"; \
	fi

capture-all: build
	@echo "Capturing all scenarios ($(RUNS) runs each)..."
	@echo "Output: $(CAPTURE_DIR)"
	@echo "========================================="
	@for scenario in $(SCENARIOS); do \
		echo ""; \
		echo "Scenario: $$scenario"; \
		mkdir -p "$(CAPTURE_DIR)/$$scenario"; \
		i=1; \
		while [ $$i -le "$(RUNS)" ]; do \
			OUTPUT_PATH="$(CAPTURE_DIR)/$$scenario/$$scenario-run$$i.gputrace"; \
			echo "  Run $$i: $$OUTPUT_PATH"; \
			MTL_CAPTURE_ENABLED=1 .build/release/trace-generator "$$scenario" "$$OUTPUT_PATH"; \
			i=$$((i + 1)); \
		done; \
	done
	@echo ""
	@echo "✅ All scenarios captured"
	@echo "   Location: $(CAPTURE_DIR)/"
	@echo "   Total traces: $$(find $(CAPTURE_DIR) -name '*.gputrace' -type d 2>/dev/null | wc -l)"

test: build
	@echo "Testing with single-encoder scenario..."
	.build/release/trace-generator 01-single-encoder

list: build
	.build/release/trace-generator list

clean:
	swift package clean
	rm -rf .build
