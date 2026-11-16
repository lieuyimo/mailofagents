package store

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	// Tests create many accounts; production bcrypt cost dominates the suite
	// under -race. Verification still exercises real bcrypt at MinCost.
	defaultBcryptCost = bcrypt.MinCost
	os.Exit(m.Run())
}
