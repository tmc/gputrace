//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/vision"
)

type summaryOCRMatch struct {
	Text       string
	Confidence float64
	X          float64
	Y          float64
	Width      float64
	Height     float64
}

type screenRect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

func (r screenRect) contains(x, y float64) bool {
	return x > r.X && y > r.Y && x < r.X+r.Width && y < r.Y+r.Height
}

func (m summaryOCRMatch) center() (float64, float64) {
	return m.X + m.Width/2, m.Y + m.Height/2
}

func clickSummaryPerformanceOCR(ctx context.Context, appAX uintptr, summary standaloneRecoveryWindow, recovery standaloneExportRecovery, geometryKey string) error {
	if err := activateProcessPID(int32(recovery.Identity.PID)); err != nil {
		return fmt.Errorf("activate bound Xcode for Summary OCR: %w", err)
	}
	if err := axAction(summary.Element, "AXRaise"); err != nil {
		return fmt.Errorf("raise selected Summary window for OCR: %w", err)
	}
	if err := waitForAutomation(ctx, 200*time.Millisecond); err != nil {
		return err
	}
	var previous summaryOCRMatch
	for sample := 0; sample < 2; sample++ {
		if err := checkAutomationCanceled(ctx); err != nil {
			return err
		}
		if err := requireRecoveryIdentity(appAX, recovery); err != nil {
			return err
		}
		current, err := summaryRecoveryTarget(recoveryWindows(appAX), recovery, geometryKey)
		if err != nil {
			return fmt.Errorf("revalidate Summary before OCR sample %d: %w", sample+1, err)
		}
		if shows := shallowShowPerformanceButtons(current.Element); len(shows) != 0 {
			return fmt.Errorf("AX Show Performance controls changed while preparing OCR")
		}
		region, err := summaryRightPaneRegion(current.Element)
		if err != nil {
			return err
		}
		match, err := recognizeSummaryPerformance(current.Element, region)
		if err != nil {
			return fmt.Errorf("Summary OCR sample %d: %w", sample+1, err)
		}
		if sample > 0 && !stableSummaryOCRMatch(previous, match, 4) {
			return fmt.Errorf("Summary OCR target moved between stable samples")
		}
		previous = match
		summary = current
		if sample == 0 {
			if err := waitForAutomation(ctx, 250*time.Millisecond); err != nil {
				return err
			}
		}
	}

	// The second sample is immediately followed by a final structural and
	// hit-test check. No additional OCR or click retry is permitted.
	current, err := summaryRecoveryTarget(recoveryWindows(appAX), recovery, geometryKey)
	if err != nil {
		return fmt.Errorf("revalidate Summary before OCR click: %w", err)
	}
	if standaloneRecoveryWindowKey(current) != standaloneRecoveryWindowKey(summary) {
		return fmt.Errorf("Summary window changed after OCR proof")
	}
	cx, cy := previous.center()
	region, err := summaryRightPaneRegion(current.Element)
	if err != nil {
		return err
	}
	if !region.contains(cx, cy) {
		return fmt.Errorf("OCR target center lies outside selected Summary right pane")
	}
	hit := axCopyElementAtPosition(appAX, cx, cy)
	if hit == 0 {
		return fmt.Errorf("cannot hit-test OCR target in selected Xcode window")
	}
	defer cfRelease(hit)
	if parent := findParentWindow(hit); parent == 0 ||
		recoveryGeometryKeyForElement(parent, recovery.Identity.PID) != geometryKey {
		return fmt.Errorf("OCR target hit-test does not belong to selected Xcode window")
	}
	if err := clickScreenPoint(cx, cy); err != nil {
		return fmt.Errorf("click OCR Show Performance target: %w", err)
	}
	return nil
}

func summaryRightPaneRegion(window uintptr) (screenRect, error) {
	wx, wy := axPosition(window)
	ww, wh := axSize(window)
	navigator := findElementAtDepth(
		window, 3, 96, axChildren,
		func(element uintptr) bool { return axString(element, "AXRole") == "AXOutline" },
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXGroup" &&
				axString(element, "AXDescription") == "navigator"
		},
	)
	debugBar := findElementAtDepth(
		window, 4, 128, axChildren,
		func(element uintptr) bool { return axString(element, "AXRole") == "AXOutline" },
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXGroup" &&
				axString(element, "AXDescription") == "debug bar"
		},
	)
	if ww <= 0 || wh <= 0 || navigator == 0 || debugBar == 0 {
		return screenRect{}, fmt.Errorf("cannot establish selected Summary right-pane bounds")
	}
	nx, _ := axPosition(navigator)
	nw, _ := axSize(navigator)
	_, debugY := axPosition(debugBar)
	left := float64(nx + nw)
	top := float64(wy + 52)
	right := float64(wx + ww)
	bottom := float64(debugY)
	if left < float64(wx) || right > float64(wx+ww) ||
		top < float64(wy) || bottom > float64(wy+wh) ||
		right-left < 200 || bottom-top < 100 {
		return screenRect{}, fmt.Errorf("invalid selected Summary right-pane bounds")
	}
	return screenRect{X: left, Y: top, Width: right - left, Height: bottom - top}, nil
}

func recognizeSummaryPerformance(window uintptr, region screenRect) (summaryOCRMatch, error) {
	windowID, err := getWindowID(window)
	if err != nil {
		return summaryOCRMatch{}, err
	}
	image := cgWindowListCreateImage(
		math.Inf(1), math.Inf(1), 0, 0,
		kCGWindowListOptionIncludingWindow,
		windowID,
		kCGWindowImageBoundsIgnoreFraming|kCGWindowImageBestResolution,
	)
	if image == 0 {
		return summaryOCRMatch{}, fmt.Errorf("capture selected Xcode window %d", windowID)
	}
	defer cgImageRelease(image)
	imageWidth := float64(coregraphics.CGImageGetWidth(coregraphics.CGImageRef(image)))
	imageHeight := float64(coregraphics.CGImageGetHeight(coregraphics.CGImageRef(image)))
	wx, wy := axPosition(window)
	ww, wh := axSize(window)
	if imageWidth <= 0 || imageHeight <= 0 || ww <= 0 || wh <= 0 {
		return summaryOCRMatch{}, fmt.Errorf("invalid selected-window image geometry")
	}

	handler := vision.NewImageRequestHandlerWithCGImageOptions(coregraphics.CGImageRef(image), nil)
	request := vision.NewVNRecognizeTextRequest()
	request.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelAccurate)
	request.SetUsesLanguageCorrection(true)
	ok, err := handler.PerformRequestsError([]vision.VNRequest{request.VNRequest})
	if err != nil {
		return summaryOCRMatch{}, fmt.Errorf("Vision OCR: %w", err)
	}
	if !ok {
		return summaryOCRMatch{}, fmt.Errorf("Vision OCR request failed")
	}

	var matches []summaryOCRMatch
	for _, observation := range request.Results() {
		text := vision.VNRecognizedTextObservationFromID(observation.ID)
		candidates := text.TopCandidates(1)
		if len(candidates) == 0 {
			continue
		}
		candidate := candidates[0]
		if normalizeOCRText(candidate.String()) != "show performance" ||
			float64(candidate.Confidence()) < 0.8 {
			continue
		}
		bounds := text.BoundingBox()
		localX := bounds.Origin.X * float64(ww)
		localY := (1 - bounds.Origin.Y - bounds.Size.Height) * float64(wh)
		match := summaryOCRMatch{
			Text:       candidate.String(),
			Confidence: float64(candidate.Confidence()),
			X:          float64(wx) + localX,
			Y:          float64(wy) + localY,
			Width:      bounds.Size.Width * float64(ww),
			Height:     bounds.Size.Height * float64(wh),
		}
		cx, cy := match.center()
		if match.Width < 40 || match.Height < 8 ||
			!region.contains(match.X, match.Y) ||
			!region.contains(match.X+match.Width, match.Y+match.Height) ||
			!region.contains(cx, cy) {
			continue
		}
		matches = append(matches, match)
	}
	if len(matches) != 1 {
		return summaryOCRMatch{}, fmt.Errorf("want one exact Show Performance OCR match in selected right pane, found %d", len(matches))
	}
	return matches[0], nil
}

func normalizeOCRText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

func stableSummaryOCRMatch(left, right summaryOCRMatch, tolerance float64) bool {
	if normalizeOCRText(left.Text) != "show performance" ||
		normalizeOCRText(right.Text) != "show performance" {
		return false
	}
	lx, ly := left.center()
	rx, ry := right.center()
	return math.Abs(lx-rx) <= tolerance &&
		math.Abs(ly-ry) <= tolerance &&
		math.Abs(left.Width-right.Width) <= tolerance &&
		math.Abs(left.Height-right.Height) <= tolerance
}

func clickScreenPoint(x, y float64) error {
	down := cgEventCreateMouseEvent(0, kCGEventLeftMouseDown, x, y, 0)
	if down == 0 {
		return fmt.Errorf("create mouse down event")
	}
	defer cfRelease(down)
	up := cgEventCreateMouseEvent(0, kCGEventLeftMouseUp, x, y, 0)
	if up == 0 {
		return fmt.Errorf("create mouse up event")
	}
	defer cfRelease(up)
	cgEventPost(kCGHIDEventTap, down)
	time.Sleep(50 * time.Millisecond)
	cgEventPost(kCGHIDEventTap, up)
	return nil
}
