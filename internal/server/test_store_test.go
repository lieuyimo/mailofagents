package server

import (
	"golang.org/x/crypto/bcrypt"

	"github.com/agentmail/agentmail/internal/store"
)

// testStoreOptions keeps password hashing cheap. Server tests make many
// authenticated HTTP calls, and bcrypt at production cost dominates them
// under -race. The cost is embedded in every hash, so verification still
// runs real bcrypt — just at MinCost.
func testStoreOptions() []store.Option {
	return []store.Option{store.WithBcryptCost(bcrypt.MinCost)}
}
