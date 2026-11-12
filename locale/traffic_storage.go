package locale

func init() {
	Register("Traffic capture paused until WAL recovery succeeds", "Traffic capture paused until WAL recovery succeeds")
	Register("Traffic recovery could not acquire rollback journal", "Traffic recovery could not acquire rollback journal")
	Register("Traffic recovery could not restore WAL", "Traffic recovery could not restore WAL")
	Register("Traffic WAL write estimate exceeds storage admission limit", "Traffic WAL write estimate exceeds storage admission limit")
	Register("Traffic WAL storage admission blocked by an active snapshot", "Traffic WAL storage admission blocked by an active snapshot")
	Register("%w (threshold=%d bytes)", "%w (threshold=%d bytes)")
	Register("COUNTERWEIGHT_TRAFFIC_WAL_BYTES must be at least 65536 bytes", "COUNTERWEIGHT_TRAFFIC_WAL_BYTES must be at least 65536 bytes")
	Register("Traffic index storage limit reached", "Traffic index storage limit reached")
	Register("%w (limit=%d bytes)", "%w (limit=%d bytes)")
	Register("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES must be at least 262144 bytes", "COUNTERWEIGHT_TRAFFIC_INDEX_BYTES must be at least 262144 bytes")
	Register("[traffic] Capture skipped; index storage admission failed: %v", "[traffic] Capture skipped; index storage admission failed: %v")
	Register("Traffic blob storage limit reached", "Traffic blob storage limit reached")
	Register("%w (limit=%d, used=%d bytes)", "%w (limit=%d, used=%d bytes)")
	Register("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES must be a positive byte count", "COUNTERWEIGHT_TRAFFIC_BLOB_BYTES must be a positive byte count")
	Register("[traffic] Capture skipped; body storage failed: %v", "[traffic] Capture skipped; body storage failed: %v")
}
