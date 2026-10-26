package auth

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestLoginLockoutRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const email = "guard-recovery@example.test"
		defer ClearLoginFailures(email)
		for range loginFailLimit - 1 {
			RecordLoginFailure("  GUARD-RECOVERY@example.test ")
		}
		if LoginLocked(email) {
			t.Fatal("locked before threshold")
		}
		RecordLoginFailure(email)
		if !LoginLocked(email) || LoginLocked("another@example.test") {
			t.Fatal("lockout scope mismatch")
		}
		time.Sleep(loginLockFor)
		if LoginLocked(email) {
			t.Fatal("lockout did not expire")
		}
		RecordLoginFailure(email)
		if LoginLocked(email) {
			t.Fatal("expired failures were retained")
		}
		ClearLoginFailures(email)
		for range loginFailLimit - 1 {
			RecordLoginFailure(email)
		}
		if LoginLocked(email) {
			t.Fatal("successful login did not reset failures")
		}
	})
}
