package db

import (
	"encoding/json"
	"testing"
)

func TestInterceptSourceLanguageMarkers(t *testing.T) {
	for _, reason := range []string{"[Model] decision"} {
		if got := interceptSource(0, reason); got != "model" {
			t.Fatalf("%q source=%s", reason, got)
		}
		if got := interceptSource(7, reason); got != "rule" {
			t.Fatalf("rule must take precedence: %s", got)
		}
	}
	if got := interceptSource(0, "User text mentioning [Model]"); got != "unknown" {
		t.Fatalf("embedded marker classified as %s", got)
	}
}

func TestInterceptSourceLanguagePersistenceAndRestore(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, reason := range []string{"[Model] decision"} {
		id, err := d.CreateInterceptPending(0, 0, "locale-classification", "test", "Read", json.RawMessage(`{}`), reason)
		if err != nil {
			t.Fatal(err)
		}
		defer func(id int64) {
			if _, err := d.Exec(`DELETE FROM intercept_pending WHERE id=$1`, id); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}(id)
		for _, stored := range []bool{true, false} {
			if !stored {
				if _, err = d.Exec(`UPDATE intercept_pending SET decision_source='' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := d.GetInterceptDetail(id)
			if err != nil || got == nil || got.DecisionSource != "model" {
				t.Fatalf("%q stored=%v: %+v %v", reason, stored, got, err)
			}
		}
		// Simulate an old archive without decision_source, preserving every other row field.
		var raw []byte
		if err = d.QueryRow(`SELECT jsonb_build_array(to_jsonb(ip)-'decision_source') FROM intercept_pending ip WHERE id=$1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		tx, err := d.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`DELETE FROM intercept_pending WHERE id=$1`, id); err == nil {
			err = restoreInterceptRows(tx, raw)
		}
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		var source string
		if err = tx.QueryRow(`SELECT decision_source FROM intercept_pending WHERE id=$1`, id).Scan(&source); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if source != "model" {
			t.Fatalf("restored %q source=%s", reason, source)
		}
	}
}
