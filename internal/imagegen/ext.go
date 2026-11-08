package imagegen

import "strings"

// imageExts maps image MIME types the image model can return to object key
// extensions.
var imageExts = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/webp": "webp",
	"image/heic": "heic",
	"image/heif": "heif",
}

// FileExt returns the object key extension for an image MIME type, ignoring
// parameters and case. An unrecognized type returns "bin"; the stored
// object's Content-Type still carries the real type.
func FileExt(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	if ext, ok := imageExts[strings.ToLower(strings.TrimSpace(base))]; ok {
		return ext
	}
	return "bin"
}
