package common

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsFrontendAssetPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "rsbuild javascript", path: "/static/js/index.abc123.js", want: true},
		{name: "assets chunk", path: "/assets/index-abc123.js", want: true},
		{name: "spa route", path: "/dashboard", want: false},
		{name: "similar prefix", path: "/static-files/app.js", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, IsFrontendAssetPath(test.path))
		})
	}
}

type testServeFileSystem struct {
	files map[string]string
}

func (f testServeFileSystem) Exists(_ string, path string) bool {
	_, ok := f.files[path]
	return ok
}

func (f testServeFileSystem) Open(name string) (http.File, error) {
	content, ok := f.files[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return &testHTTPFile{Reader: strings.NewReader(content)}, nil
}

type testHTTPFile struct {
	*strings.Reader
}

func (f *testHTTPFile) Close() error                       { return nil }
func (f *testHTTPFile) Readdir(int) ([]fs.FileInfo, error) { return nil, nil }
func (f *testHTTPFile) Stat() (fs.FileInfo, error)         { return nil, nil }

func TestThemeAwareFileSystemFallsBackAcrossThemes(t *testing.T) {
	originalTheme := GetTheme()
	t.Cleanup(func() {
		SetTheme(originalTheme)
	})

	defaultFS := testServeFileSystem{
		files: map[string]string{"/static/js/default.js": "default"},
	}
	classicFS := testServeFileSystem{
		files: map[string]string{"/static/js/classic.js": "classic"},
	}
	themeFS := NewThemeAwareFS(defaultFS, classicFS)

	SetTheme("classic")
	assert.True(t, themeFS.Exists("", "/static/js/default.js"))
	file, err := themeFS.Open("/static/js/default.js")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	content, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, "default", string(content))

	SetTheme("default")
	assert.True(t, themeFS.Exists("", "/static/js/classic.js"))
}
