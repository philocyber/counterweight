package server

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// HTTP fixtures need the same credential-bound sessions as real login. The
// isolated test database's prior credential is restored after each fixture.
func signTestSession(t *testing.T, s *Server) (string, error) {
	t.Helper()
	hash, existed, err := s.m.pg.GetSetting(authPassKey)
	if err != nil {
		return "", err
	}
	if hash == "" {
		generated, err := bcrypt.GenerateFromPassword([]byte("fixture-only-password"), bcrypt.MinCost)
		if err != nil {
			return "", err
		}
		hash = string(generated)
		if err := s.m.pg.SetSetting(authPassKey, hash); err != nil {
			return "", err
		}
		t.Cleanup(func() {
			if existed {
				_ = s.m.pg.SetSetting(authPassKey, "")
			} else {
				_, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey)
			}
		})
	}
	return signCredentialJWT(s.jwtKey, hash)
}
