package workspacevolumecopy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	volcopycore "github.com/coder/coder/v2/workspacevolumecopy"
)

func TestClientListWorkspaceVolumes(t *testing.T) {
	t.Parallel()

	workspaceID := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/namespaces/coder-workspaces/persistentvolumeclaims", r.URL.Path)
		require.Equal(t, workspaceIDLabel+"="+workspaceID.String(), r.URL.Query().Get("labelSelector"))
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"items": []any{
				map[string]any{
					"metadata": map[string]any{
						"name": "coder-home",
						"annotations": map[string]string{
							volcopycore.AnnotationLogicalKey: "home",
							volcopycore.AnnotationOwnerUID:   "1000",
							volcopycore.AnnotationOwnerGID:   "1001",
						},
					},
					"spec":   map[string]any{"resources": map[string]any{"requests": map[string]string{"storage": "20Gi"}}},
					"status": map[string]any{"capacity": map[string]string{"storage": "20Gi"}},
				},
				map[string]any{
					"metadata": map[string]any{
						"name":        "ignored",
						"annotations": map[string]string{},
					},
				},
			},
		})
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	client := NewClient(baseURL, "token", server.Client())
	volumes, err := client.ListWorkspaceVolumes(context.Background(), "coder-workspaces", workspaceID)
	require.NoError(t, err)
	ownerUID := uint32(1000)
	ownerGID := uint32(1001)
	require.Equal(t, []Volume{{
		Key:         "home",
		DisplayName: "Home",
		MountPath:   "",
		Capacity:    "20Gi",
		ClaimName:   "coder-home",
		OwnerUID:    &ownerUID,
		OwnerGID:    &ownerGID,
	}}, volumes)
}

func TestBuildJobUsesReadOnlySourceAndPinnedImage(t *testing.T) {
	t.Parallel()

	operationID := uuid.New()
	sourceUID := uint32(1000)
	sourceGID := uint32(1000)
	destinationUID := uint32(2000)
	destinationGID := uint32(3000)
	job, err := buildJob("coder-workspaces", "copy-job", "ghcr.io/biptec/coder-volume-copy@sha256:deadbeef", operationID, true, []JobVolume{{
		Key:                 "home",
		SourceClaim:         "source-home",
		DestinationClaim:    "destination-home",
		Overwrite:           true,
		ExcludedPaths:       []string{".ssh/id_ed25519_workspace"},
		SourceOwnerUID:      &sourceUID,
		SourceOwnerGID:      &sourceGID,
		DestinationOwnerUID: &destinationUID,
		DestinationOwnerGID: &destinationGID,
	}})
	require.NoError(t, err)

	spec := job["spec"].(map[string]any)
	template := spec["template"].(map[string]any)
	podSpec := template["spec"].(map[string]any)
	require.Equal(t, false, podSpec["automountServiceAccountToken"])
	containers := podSpec["containers"].([]any)
	container := containers[0].(map[string]any)
	require.Equal(t, "ghcr.io/biptec/coder-volume-copy@sha256:deadbeef", container["image"])
	require.Equal(t, "IfNotPresent", container["imagePullPolicy"])
	require.Equal(t, []string{"/opt/coder-volume-copy-helper"}, container["command"])
	securityContext := container["securityContext"].(map[string]any)
	require.EqualValues(t, 0, securityContext["runAsUser"])
	require.EqualValues(t, 0, securityContext["runAsGroup"])
	require.Equal(t, false, securityContext["allowPrivilegeEscalation"])
	require.Equal(t, true, securityContext["readOnlyRootFilesystem"])
	capabilities := securityContext["capabilities"].(map[string]any)
	require.Equal(t, []string{"ALL"}, capabilities["drop"])
	require.Equal(t, []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETFCAP"}, capabilities["add"])

	mounts := container["volumeMounts"].([]any)
	require.Equal(t, true, mounts[0].(map[string]any)["readOnly"])
	_, hasDestinationReadOnly := mounts[1].(map[string]any)["readOnly"]
	require.False(t, hasDestinationReadOnly)

	env := container["env"].([]any)
	rawPlan := env[0].(map[string]any)["value"].(string)
	var plan struct {
		AllowSourceChanges bool `json:"allow_source_changes"`
		Volumes            []struct {
			Overwrite           bool     `json:"overwrite"`
			Excluded            []string `json:"excluded_paths"`
			SourceOwnerUID      *uint32  `json:"source_owner_uid"`
			SourceOwnerGID      *uint32  `json:"source_owner_gid"`
			DestinationOwnerUID *uint32  `json:"destination_owner_uid"`
			DestinationOwnerGID *uint32  `json:"destination_owner_gid"`
		} `json:"volumes"`
	}
	require.NoError(t, json.Unmarshal([]byte(rawPlan), &plan))
	require.True(t, plan.AllowSourceChanges)
	require.True(t, plan.Volumes[0].Overwrite)
	require.Equal(t, []string{".ssh/id_ed25519_workspace"}, plan.Volumes[0].Excluded)
	require.Equal(t, sourceUID, *plan.Volumes[0].SourceOwnerUID)
	require.Equal(t, sourceGID, *plan.Volumes[0].SourceOwnerGID)
	require.Equal(t, destinationUID, *plan.Volumes[0].DestinationOwnerUID)
	require.Equal(t, destinationGID, *plan.Volumes[0].DestinationOwnerGID)
}

func TestEnsureCopyJobIsIdempotentOnConflict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"kind":"Job"`)
		http.Error(rw, `{"reason":"AlreadyExists"}`, http.StatusConflict)
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := NewClient(baseURL, "", server.Client())

	err = client.EnsureCopyJob(context.Background(), "coder-workspaces", "copy-job", "image@sha256:deadbeef", uuid.New(), false, []JobVolume{{
		Key:              "home",
		SourceClaim:      "source-home",
		DestinationClaim: "destination-home",
	}})
	require.NoError(t, err)
}

func TestGetCopyJobState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"status": map[string]any{
				"failed": 1,
				"conditions": []any{map[string]any{
					"type": "Failed", "status": "True", "reason": "DeadlineExceeded", "message": "copy timed out",
				}},
			},
		})
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := NewClient(baseURL, "", server.Client())

	state, err := client.GetCopyJobState(context.Background(), "coder-workspaces", "copy-job")
	require.NoError(t, err)
	require.True(t, state.Failed)
	require.Contains(t, state.Message, "DeadlineExceeded")
}
