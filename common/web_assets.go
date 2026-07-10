package common

import "strings"

// IsFrontendAssetPath reports whether path belongs to a content-hashed
// frontend build directory. Both paths are supported because Rsbuild versions
// and project configurations may emit either layout.
func IsFrontendAssetPath(path string) bool {
	return strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/static/")
}
