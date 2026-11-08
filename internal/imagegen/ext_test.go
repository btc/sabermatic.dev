package imagegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileExt(t *testing.T) {
	for mimeType, want := range map[string]string{
		"image/png":                "png",
		"image/jpeg":               "jpg",
		"IMAGE/JPEG":               "jpg",
		"image/webp; quality=high": "webp",
		"image/heic":               "heic",
		"image/gif":                "bin",
		"":                         "bin",
	} {
		assert.Equal(t, want, FileExt(mimeType), mimeType)
	}
}
