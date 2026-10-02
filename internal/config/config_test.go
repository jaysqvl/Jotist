package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDatabasePathUsesJotistDefaultOrExplicitExistingPath(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("DATABASE_PATH", "")
	require.Equal(t, "data/jotist.db", Load().DatabasePath)

	// Existing data is selected only through an explicitly configured path.
	t.Setenv("DATABASE_PATH", "/existing/data/custom.db")
	require.Equal(t, "/existing/data/custom.db", Load().DatabasePath)
}
