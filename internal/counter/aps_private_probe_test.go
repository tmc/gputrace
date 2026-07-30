//go:build darwin && gputrace_private_bindings

package counter

import (
	"os"
	"testing"

	"github.com/tmc/apple/metal"
)

// TestProbeAPSCounterProfiles is opt-in because creating a private APS source
// is a host capability probe, not a portable unit test. It never starts
// sampling or modifies the source configuration.
func TestProbeAPSCounterProfiles(t *testing.T) {
	if os.Getenv("GPUTRACE_APS_PROFILE_PROBE") == "" {
		t.Skip("set GPUTRACE_APS_PROFILE_PROBE=1 to probe host APS profiles")
	}
	device := metal.MTLCreateSystemDefaultDevice()
	if device.GetID() == 0 {
		t.Skip("Metal device is unavailable")
	}
	source, err := NewAPSDataSourceWithDedicatedQueue(device.GetID(), "gputrace.aps.profile-probe")
	if err != nil {
		t.Skipf("APS data source is unavailable: %v", err)
	}
	defer source.Release()
	profiles, err := source.SupportedCounterProfileInfo()
	if err != nil {
		t.Skipf("APS profiles are unavailable: %v", err)
	}
	for _, profile := range profiles {
		t.Logf("APS profile=%d name=%q isAPS=%t", profile.Profile, profile.Name, profile.IsAPS)
	}
}
