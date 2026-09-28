package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenCacheFile(t *testing.T) {
	path := TokenCacheFile("https://example.awsapps.com/start")
	require.Contains(t, path, "e8be5486177c5b5392bd9aa76563515b29358e6e.json")
}
