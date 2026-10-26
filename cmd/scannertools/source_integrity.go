package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func sourceSHA256(client *http.Client, source string) (string, error) {
	if !strings.HasPrefix(source, "https://") {
		return "", fmt.Errorf("source artifact must use HTTPS")
	}
	// Do not permit an HTTPS source to redirect to plaintext transport.
	clientCopy := *client
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 10 {
			return fmt.Errorf("unsafe artifact redirect")
		}
		return nil
	}
	resp, err := clientCopy.Get(source)
	if err != nil {
		return "", fmt.Errorf("download scanner artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("scanner artifact returned HTTP %d", resp.StatusCode)
	}
	const maxBytes = 2 << 30
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("hash scanner artifact: %w", err)
	}
	if n == 0 || n > maxBytes {
		return "", fmt.Errorf("scanner artifact is empty or exceeds 2 GiB")
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
