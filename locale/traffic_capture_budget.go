package locale

func init() {
	Register("[traffic] Capture skipped; cannot generate exchange ID: %v", "[traffic] Capture skipped; cannot generate exchange ID: %v")
	Register("Traffic capture exceeds exchange or host limit", "Traffic capture exceeds exchange or host limit")
	Register("Traffic retained exchange limit reached", "Traffic retained exchange limit reached")
	Register("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES must be a positive exchange count", "COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES must be a positive exchange count")
	Register("%s must be a positive byte count", "%s must be a positive byte count")
	Register("[traffic] Capture skipped; capture quota admission failed: %v", "[traffic] Capture skipped; capture quota admission failed: %v")
}
