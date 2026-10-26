package container

import "sync"

// External scanners (registered through pkg/scannerapi, e.g. customer-licensed
// commercial engines) bring their own image. Operator ImageOverrides still win.
var externalImages sync.Map // tool -> image

// RegisterExternalImage records the image an externally registered scanner runs.
func RegisterExternalImage(tool, image string) { externalImages.Store(tool, image) }

func externalImage(tool string) string {
	if v, ok := externalImages.Load(tool); ok {
		return v.(string)
	}
	return ""
}

func externalImageList() []string {
	var out []string
	externalImages.Range(func(_, v any) bool {
		out = append(out, v.(string))
		return true
	})
	return out
}
