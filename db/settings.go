package db

import "database/sql"

// Settings is a tiny key-value store for global app config the UI toggles at
// runtime (e.g. traffic_capture). Missing keys fall back to caller defaults.

// GetSetting returns the stored value and ok=false when the key is unset.
func (d *DB) GetSetting(key string) (value string, ok bool, err error) {
	err = d.QueryRow(`SELECT value FROM settings WHERE key=$1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if isSealedSetting(key) {
		if value, err = openSecret("settings", "value", key, value); err != nil {
			return "", false, err
		}
	}
	return value, true, nil
}

// SetSetting upserts a setting value. Credential settings (sealedSettings)
// are stored encrypted.
func (d *DB) SetSetting(key, value string) error {
	if isSealedSetting(key) {
		var err error
		if value, err = sealSecret("settings", "value", key, value); err != nil {
			return err
		}
	}
	_, err := d.Exec(`
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// InitializeSetting installs a value only when the setting is missing or empty.
// The database enforces the condition atomically across concurrent servers.
func (d *DB) InitializeSetting(key, value string) (bool, error) {
	result, err := d.Exec(`
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
WHERE settings.value = ''`, key, value)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// CompareAndSwapSetting replaces an existing value only if it is still the one
// the caller authenticated. Concurrent credential resets must not be overwritten.
func (d *DB) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	result, err := d.Exec(`UPDATE settings SET value=$3, updated_at=now() WHERE key=$1 AND value=$2`, key, expected, value)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// GetBool returns the boolean setting, or def when unset/unparseable.
func (d *DB) GetBool(key string, def bool) bool {
	v, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		return def
	}
	return v == "true" || v == "1"
}

// SetBool stores a boolean setting as "true"/"false".
func (d *DB) SetBool(key string, val bool) error {
	if val {
		return d.SetSetting(key, "true")
	}
	return d.SetSetting(key, "false")
}
