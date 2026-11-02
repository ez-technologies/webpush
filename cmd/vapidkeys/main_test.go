package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ez-technologies/webpush"
)

func TestRun_Env(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, run(&out, "env", "APP_VAPID_"))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	pub, ok := strings.CutPrefix(lines[0], "APP_VAPID_PUBLIC_KEY=")
	require.True(t, ok, lines[0])
	priv, ok := strings.CutPrefix(lines[1], "APP_VAPID_PRIVATE_KEY=")
	require.True(t, ok, lines[1])
	assert.NoError(t, webpush.VAPIDKeys{Public: pub, Private: priv}.Validate())
}

func TestRun_JSON(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, run(&out, "json", ""))
	var keys webpush.VAPIDKeys
	require.NoError(t, json.Unmarshal(out.Bytes(), &keys))
	assert.NoError(t, keys.Validate())
}

func TestRun_UnknownFormat(t *testing.T) {
	assert.Error(t, run(&bytes.Buffer{}, "yaml", ""))
}
