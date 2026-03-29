package externalauth_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"

	"github.com/coder/coder/v2/coderd/externalauth"
)

func TestRegistry_NewRegistry(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	cfgA := &externalauth.Config{ID: "github"}
	cfgB := &externalauth.Config{ID: "gitlab"}
	reg := externalauth.NewRegistry(logger, []*externalauth.Config{cfgA, cfgB})

	require.Equal(t, 2, reg.Len())

	got, ok := reg.Get("github")
	require.True(t, ok)
	require.Same(t, cfgA, got)

	got, ok = reg.Get("gitlab")
	require.True(t, ok)
	require.Same(t, cfgB, got)
}

func TestRegistry_Get_Existing(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	cfg := &externalauth.Config{ID: "bitbucket"}
	reg := externalauth.NewRegistry(logger, []*externalauth.Config{cfg})

	got, ok := reg.Get("bitbucket")
	require.True(t, ok)
	require.Same(t, cfg, got)
}

func TestRegistry_Get_NotFound(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	reg := externalauth.NewRegistry(logger, []*externalauth.Config{
		{ID: "github"},
	})

	got, ok := reg.Get("nonexistent")
	require.False(t, ok)
	require.Nil(t, got)
}

func TestRegistry_List(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	cfgA := &externalauth.Config{ID: "a"}
	cfgB := &externalauth.Config{ID: "b"}
	cfgC := &externalauth.Config{ID: "c"}
	reg := externalauth.NewRegistry(logger, []*externalauth.Config{cfgA, cfgB, cfgC})

	list := reg.List()
	require.Len(t, list, 3)
	// Verify insertion order is preserved.
	require.Same(t, cfgA, list[0])
	require.Same(t, cfgB, list[1])
	require.Same(t, cfgC, list[2])

	// Verify it's a copy — mutating the returned slice must not
	// affect the registry.
	list[0] = nil
	list2 := reg.List()
	require.Same(t, cfgA, list2[0], "List should return a fresh copy")
}

func TestRegistry_Len(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	reg := externalauth.NewRegistry(logger, nil)
	require.Equal(t, 0, reg.Len())

	reg.Replace([]*externalauth.Config{{ID: "a"}, {ID: "b"}})
	require.Equal(t, 2, reg.Len())
}

func TestRegistry_Replace(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	old := &externalauth.Config{ID: "old"}
	reg := externalauth.NewRegistry(logger, []*externalauth.Config{old})

	newA := &externalauth.Config{ID: "new-a"}
	newB := &externalauth.Config{ID: "new-b"}
	reg.Replace([]*externalauth.Config{newA, newB})

	require.Equal(t, 2, reg.Len())

	// Old config should be gone.
	_, ok := reg.Get("old")
	require.False(t, ok)

	// New configs should be present.
	got, ok := reg.Get("new-a")
	require.True(t, ok)
	require.Same(t, newA, got)

	got, ok = reg.Get("new-b")
	require.True(t, ok)
	require.Same(t, newB, got)
}

func TestRegistry_Reload_NoDeps(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	reg := externalauth.NewRegistry(logger, nil)

	err := reg.Reload(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reload deps not set")
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, nil)

	configs := make([]*externalauth.Config, 10)
	for i := range configs {
		configs[i] = &externalauth.Config{ID: fmt.Sprintf("provider-%d", i)}
	}
	reg := externalauth.NewRegistry(logger, configs)

	const goroutines = 20
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines * 3) // Get, List, Replace goroutines

	// Concurrent Get calls.
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, _ = reg.Get(fmt.Sprintf("provider-%d", i%10))
			}
		}()
	}

	// Concurrent List calls.
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				list := reg.List()
				_ = len(list)
			}
		}()
	}

	// Concurrent Replace calls.
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				reg.Replace([]*externalauth.Config{
					{ID: fmt.Sprintf("replaced-%d", i)},
				})
			}
		}()
	}

	wg.Wait()

	// After all goroutines finish, the registry should be in a
	// consistent state — Len should match List length.
	require.Equal(t, reg.Len(), len(reg.List()))
}
