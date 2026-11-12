package server

import "testing"

func TestTrafficCaptureToggleReachesRecorderAndPersistedStartup(t *testing.T) {
	settings, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer settings.Close()
	prior := settings.TrafficEnabled()
	defer settings.SetTrafficEnabled(prior)
	if err := settings.SetTrafficEnabled(false); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.traffic == nil {
		t.Fatal("recording proxy was not initialized")
	}
	if m.TrafficEnabled() || m.traffic.CaptureEnabled() {
		t.Fatal("persisted capture-off did not disable recording at startup")
	}
	for _, on := range []bool{true, false, true, false} {
		if err := m.SetTrafficEnabled(on); err != nil {
			t.Fatal(err)
		}
		if m.TrafficEnabled() != on || m.traffic.CaptureEnabled() != on {
			t.Fatalf("capture %v disagrees between manager and recorder", on)
		}
		if got := m.pg.GetBool(settingTrafficCapture, !on); got != on {
			t.Fatal("capture state was not persisted")
		}
	}
}
