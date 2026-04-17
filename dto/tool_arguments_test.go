package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeConcatenatedSameJSONArgs(t *testing.T) {
	t.Run("single valid json unchanged", func(t *testing.T) {
		in := `{"location":"Beijing"}`
		out, changed := NormalizeConcatenatedSameJSONArgs(in)
		require.False(t, changed)
		require.Equal(t, in, out)
	})

	t.Run("duplicated concatenated json normalized", func(t *testing.T) {
		in := `{"location":"Beijing"}{"location":"Beijing"}`
		out, changed := NormalizeConcatenatedSameJSONArgs(in)
		require.True(t, changed)
		require.Equal(t, `{"location":"Beijing"}`, out)
	})

	t.Run("duplicated concatenated with spaces normalized", func(t *testing.T) {
		in := ` {"location":"Beijing"}  {"location":"Beijing"} `
		out, changed := NormalizeConcatenatedSameJSONArgs(in)
		require.True(t, changed)
		require.Equal(t, `{"location":"Beijing"}`, out)
	})

	t.Run("different concatenated json not changed", func(t *testing.T) {
		in := `{"location":"Beijing"}{"unit":"c"}`
		out, changed := NormalizeConcatenatedSameJSONArgs(in)
		require.False(t, changed)
		require.Equal(t, in, out)
	})

	t.Run("invalid payload not changed", func(t *testing.T) {
		in := `{"location":"Beijing"}{"location":"Beijing"`
		out, changed := NormalizeConcatenatedSameJSONArgs(in)
		require.False(t, changed)
		require.Equal(t, in, out)
	})
}
