package container

// GoEnv gives Go analyzers a writable, shared module/build cache. Go's cache
// locking supports concurrent commands; separate containers need not download
// and compile the same dependencies again. Source mounts remain read-only.
func GoEnv(cfg *Config) map[string]string {
	cache := "/tmp/wolf-go"
	if cfg != nil && cfg.DBVolume != "" {
		cache = "/var/lib/wolf-db/go"
	}
	return map[string]string{
		"HOME":        "/tmp",
		"PATH":        "/usr/local/go-toolchain/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"GOTOOLCHAIN": "local", "GOFLAGS": "-buildvcs=false",
		"GOMODCACHE": cache + "/mod", "GOCACHE": cache + "/build",
	}
}
