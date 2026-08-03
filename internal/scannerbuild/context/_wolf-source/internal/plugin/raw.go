package plugin

import "github.com/alphabravocompany/thewolf/internal/models"

// SaveRaw is the plugin-facing helper for forwarding a tool's verbatim output
// to the runner so it can be persisted to disk. It is a no-op if no raw-output
// callback has been registered on opts, or if data is empty.
//
// ext is a content-type hint ("json", "sarif", "xml", "txt"). The runner picks
// the on-disk filename; the plugin only needs to declare what shape the bytes
// are in so the file gets a sensible extension.
func SaveRaw(opts models.ExecuteOpts, data []byte, ext string) {
	if opts.OnRawOutput == nil || len(data) == 0 {
		return
	}
	opts.OnRawOutput(data, ext)
}
