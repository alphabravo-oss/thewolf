package auth

import (
	"strings"
	"sync"
	"time"
)

// ponytail: process-local mutex map; upgrade to shared store if we run multiple API instances.
const loginFailLimit = 10
const loginLockFor = 15 * time.Minute

type loginState struct {
	fails int
	until time.Time
}

var (
	loginMu    sync.Mutex
	loginTries = map[string]loginState{}
)

func loginEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// LoginLocked reports whether this email is inside a 15-minute lockout window.
func LoginLocked(email string) bool {
	email = loginEmail(email)
	loginMu.Lock()
	defer loginMu.Unlock()
	st, ok := loginTries[email]
	if !ok || st.until.IsZero() {
		return false
	}
	if time.Now().Before(st.until) {
		return true
	}
	delete(loginTries, email)
	return false
}

// RecordLoginFailure counts a failed password verify. The 10th failure locks
// the email for loginLockFor.
func RecordLoginFailure(email string) {
	email = loginEmail(email)
	loginMu.Lock()
	defer loginMu.Unlock()
	st := loginTries[email]
	if !st.until.IsZero() && time.Now().Before(st.until) {
		return
	}
	st.fails++
	if st.fails >= loginFailLimit {
		st.until = time.Now().Add(loginLockFor)
	}
	loginTries[email] = st
}

// ClearLoginFailures resets the counter after a successful password verify.
func ClearLoginFailures(email string) {
	email = loginEmail(email)
	loginMu.Lock()
	defer loginMu.Unlock()
	delete(loginTries, email)
}
