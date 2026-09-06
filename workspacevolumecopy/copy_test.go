package workspacevolumecopy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRsyncArgsMergeWithoutOverwrite(t *testing.T) {
	t.Parallel()

	args, err := rsyncArgs(VolumePlan{
		Key:           "home",
		Source:        "/copy/source/0",
		Destination:   "/copy/destination/0",
		Overwrite:     false,
		ExcludedPaths: []string{".ssh/id_ed25519_workspace", ".local/state/coder"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"-aHAX",
		"--numeric-ids",
		"--out-format=__CODER_VOLUME_COPY__%i|%n",
		"--no-devices",
		"--no-specials",
		"--human-readable",
		"--stats",
		"--ignore-existing",
		"--exclude=/.ssh/id_ed25519_workspace",
		"--exclude=/.local/state/coder",
		"/copy/source/0/",
		"/copy/destination/0/",
	}, args)
	require.NotContains(t, args, "--delete")
}

func TestRsyncArgsOwnershipTranslation(t *testing.T) {
	t.Parallel()

	sourceUID := uint32(1000)
	sourceGID := uint32(1000)
	destinationUID := uint32(2000)
	destinationGID := uint32(3000)
	args, err := rsyncArgs(VolumePlan{
		Key:                 "home",
		Source:              "/source",
		Destination:         "/destination",
		Overwrite:           true,
		SourceOwnerUID:      &sourceUID,
		SourceOwnerGID:      &sourceGID,
		DestinationOwnerUID: &destinationUID,
		DestinationOwnerGID: &destinationGID,
	})
	require.NoError(t, err)
	require.Contains(t, args, "--usermap=1000:2000")
	require.Contains(t, args, "--groupmap=1000:3000")

	root := uint32(0)
	args, err = rsyncArgs(VolumePlan{
		Key:                 "root-data",
		Source:              "/source",
		Destination:         "/destination",
		Overwrite:           true,
		SourceOwnerUID:      &root,
		SourceOwnerGID:      &root,
		DestinationOwnerUID: &destinationUID,
		DestinationOwnerGID: &destinationGID,
	})
	require.NoError(t, err)
	require.NotContains(t, args, "--usermap=0:2000")
	require.NotContains(t, args, "--groupmap=0:3000")
}

func TestRsyncArgsOverwrite(t *testing.T) {
	t.Parallel()

	args, err := rsyncArgs(VolumePlan{
		Key:         "home",
		Source:      "/source",
		Destination: "/destination",
		Overwrite:   true,
	})
	require.NoError(t, err)
	require.NotContains(t, args, "--ignore-existing")
	require.NotContains(t, args, "--delete")
}

func TestRsyncArgsRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []VolumePlan{
		{Key: "home", Source: "relative", Destination: "/destination"},
		{Key: "home", Source: "/source", Destination: "relative"},
		{Key: "home", Source: "/same", Destination: "/same"},
		{Key: "home", Source: "/source", Destination: "/destination", ExcludedPaths: []string{"/absolute"}},
		{Key: "home", Source: "/source", Destination: "/destination", ExcludedPaths: []string{"../outside"}},
		{Key: "home", Source: "/source", Destination: "/destination", ExcludedPaths: []string{"."}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.Source+"->"+test.Destination, func(t *testing.T) {
			t.Parallel()
			_, err := rsyncArgs(test)
			require.Error(t, err)
		})
	}
}
