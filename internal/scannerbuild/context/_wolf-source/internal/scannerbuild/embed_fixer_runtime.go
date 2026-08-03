//go:build fixer_runtime

package scannerbuild

import "errors"

// Materialize is intentionally unavailable in the fixer worker binary. The
// managed API embeds the complete custom-build context, including the source
// needed to compile a fixer. Embedding that context again inside the fixer
// binary would create an unbounded recursive build context.
func Materialize(string) error {
	return errors.New("scanner custom-build context is unavailable in fixer runtime")
}
