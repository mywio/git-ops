package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecretMarshalJSON(t *testing.T) {
	secret := Secret{Value: "supersecret"}
	data, err := json.Marshal(secret)
	assert.NoError(t, err)
	assert.Equal(t, "\"REDACTED\"", string(data))

	empty := Secret{}
	data, err = json.Marshal(empty)
	assert.NoError(t, err)
	assert.Equal(t, "\"\"", string(data))
}
