package main

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"os/exec"
)

func main() {
	http.HandleFunc("/run", handleRun)
	http.ListenAndServe(":8080", nil)
}

// Command injection vulnerability
func handleRun(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query().Get("cmd")
	// BAD: Command injection — user input in exec
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write(out)
}

// Weak cryptography
func hashPassword(password string) string {
	// BAD: MD5 is not suitable for password hashing
	h := md5.Sum([]byte(password))
	return fmt.Sprintf("%x", h)
}
