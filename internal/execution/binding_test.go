package execution

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChildJobCannotBindParentExecution(t *testing.T) {
	calls := 0
	ctx := WithBinding(context.Background(), Binding{JobID: "parent", BindExecution: func(context.Context, string) error { calls++; return nil }})
	_, ok := ForJob(ctx, "child")
	require.False(t, ok)
	require.NoError(t, RegisterExecution(ctx, "child", "child-exec"))
	require.Zero(t, calls)
	require.NoError(t, RegisterExecution(ctx, "parent", "parent-exec"))
	require.Equal(t, 1, calls)
}
