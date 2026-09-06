//go:build linux

package workspacevolumecopy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemapACLText(t *testing.T) {
	t.Parallel()

	sourceUID := uint32(1000)
	destinationUID := uint32(2000)
	sourceGID := uint32(1001)
	destinationGID := uint32(3000)
	input := `# file: /copy/destination/0/file
# owner: 2000
# group: 3000
user::rw-
user:1000:r--
group::r--
group:1001:rw-
mask::rw-
other::---

# file: /copy/destination/0/dir
# owner: 2000
# group: 3000
user::rwx
default:user::rwx
default:user:1000:r-x
default:group::r-x
default:group:1001:r--
default:mask::r-x
default:other::---
`
	got, changed, err := remapACLText(input, VolumePlan{
		SourceOwnerUID:      &sourceUID,
		DestinationOwnerUID: &destinationUID,
		SourceOwnerGID:      &sourceGID,
		DestinationOwnerGID: &destinationGID,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, got, "user:2000:r--")
	require.Contains(t, got, "group:3000:rw-")
	require.Contains(t, got, "default:user:2000:r-x")
	require.Contains(t, got, "default:group:3000:r--")
	require.NotContains(t, got, "user:1000:")
	require.NotContains(t, got, "group:1001:")
}

func TestRemapACLTextRejectsPrincipalCollision(t *testing.T) {
	t.Parallel()

	sourceUID := uint32(1000)
	destinationUID := uint32(2000)
	_, _, err := remapACLText("user:1000:r--\nuser:2000:rw-\n", VolumePlan{
		SourceOwnerUID:      &sourceUID,
		DestinationOwnerUID: &destinationUID,
	})
	require.ErrorContains(t, err, "collides")
}

func TestParseRsyncTransferredPaths(t *testing.T) {
	t.Parallel()

	paths, err := parseRsyncTransferredPaths("__CODER_VOLUME_COPY__>f+++++++++|normal\n__CODER_VOLUME_COPY__>f+++++++++|line\\#012break\nNumber of files: 2\n")
	require.NoError(t, err)
	require.Equal(t, []string{"normal", "line\nbreak"}, paths)
}
