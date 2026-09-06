package coderd_test

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	volcopyk8s "github.com/coder/coder/v2/coderd/workspacevolumecopy"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisioner/echo"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/serpent"
)

type fakeWorkspaceVolumeCopyKubernetes struct {
	mu          sync.Mutex
	volumes     map[uuid.UUID][]volcopyk8s.Volume
	jobs        map[string]volcopyk8s.JobState
	jobVolumes  map[uuid.UUID][]volcopyk8s.JobVolume
	autoSucceed bool
}

func newFakeWorkspaceVolumeCopyKubernetes() *fakeWorkspaceVolumeCopyKubernetes {
	return &fakeWorkspaceVolumeCopyKubernetes{
		volumes:    make(map[uuid.UUID][]volcopyk8s.Volume),
		jobs:       make(map[string]volcopyk8s.JobState),
		jobVolumes: make(map[uuid.UUID][]volcopyk8s.JobVolume),
	}
}

func (f *fakeWorkspaceVolumeCopyKubernetes) ListWorkspaceVolumes(_ context.Context, _ string, workspaceID uuid.UUID) ([]volcopyk8s.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]volcopyk8s.Volume(nil), f.volumes[workspaceID]...), nil
}

func (f *fakeWorkspaceVolumeCopyKubernetes) EnsureCopyJob(_ context.Context, _, jobName, _ string, operationID uuid.UUID, _ bool, volumes []volcopyk8s.JobVolume) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.jobs[jobName]; !ok {
		f.jobs[jobName] = volcopyk8s.JobState{Succeeded: f.autoSucceed}
	}
	f.jobVolumes[operationID] = append([]volcopyk8s.JobVolume(nil), volumes...)
	return nil
}

func (f *fakeWorkspaceVolumeCopyKubernetes) GetCopyJobState(_ context.Context, _, jobName string) (volcopyk8s.JobState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.jobs[jobName]
	if !ok {
		return volcopyk8s.JobState{}, volcopyk8s.ErrNotFound
	}
	return state, nil
}

func (f *fakeWorkspaceVolumeCopyKubernetes) setWorkspaceVolumes(workspaceID uuid.UUID, claimName string, excluded ...string) {
	f.setWorkspaceVolume(workspaceID, volcopyk8s.Volume{
		Key:           "home",
		DisplayName:   "Home",
		MountPath:     "/home/coder",
		Capacity:      "20Gi",
		ClaimName:     claimName,
		ExcludedPaths: append([]string(nil), excluded...),
	})
}

func (f *fakeWorkspaceVolumeCopyKubernetes) setWorkspaceVolume(workspaceID uuid.UUID, volume volcopyk8s.Volume) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes[workspaceID] = []volcopyk8s.Volume{volume}
}

func (f *fakeWorkspaceVolumeCopyKubernetes) volumesForOperation(operationID uuid.UUID) []volcopyk8s.JobVolume {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]volcopyk8s.JobVolume(nil), f.jobVolumes[operationID]...)
}

func (f *fakeWorkspaceVolumeCopyKubernetes) completeAllJobs() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoSucceed = true
	for name := range f.jobs {
		f.jobs[name] = volcopyk8s.JobState{Succeeded: true}
	}
}

func TestWorkspaceVolumeCopyCrossOwnerLocksLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kubernetes := newFakeWorkspaceVolumeCopyKubernetes()
	client, db := coderdtest.NewWithDatabase(t, &coderdtest.Options{
		IncludeProvisionerDaemon: true,
		DeploymentValues: coderdtest.DeploymentValues(t, func(values *codersdk.DeploymentValues) {
			values.WorkspaceVolumeCopyEnabled = serpent.Bool(true)
			values.WorkspaceVolumeCopyNamespace = serpent.String("coder-workspaces")
			values.WorkspaceVolumeCopyImage = serpent.String("ghcr.io/biptec/coder@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		}),
		WorkspaceVolumeCopyKubernetes: kubernetes,
	})
	owner := coderdtest.CreateFirstUser(t, client)
	memberClient, _ := coderdtest.CreateAnotherUser(t, client, owner.OrganizationID)

	version := coderdtest.CreateTemplateVersion(t, client, owner.OrganizationID, &echo.Responses{Parse: echo.ParseComplete})
	coderdtest.AwaitTemplateVersionJobCompleted(t, client, version.ID)
	template := coderdtest.CreateTemplate(t, client, owner.OrganizationID, version.ID)

	source := coderdtest.CreateWorkspace(t, client, template.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, source.LatestBuild.ID)
	source = coderdtest.MustTransitionWorkspace(t, client, source.ID, codersdk.WorkspaceTransitionStart, codersdk.WorkspaceTransitionStop)

	destination := coderdtest.CreateWorkspace(t, memberClient, template.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, memberClient, destination.LatestBuild.ID)
	destination = coderdtest.MustTransitionWorkspace(t, memberClient, destination.ID, codersdk.WorkspaceTransitionStart, codersdk.WorkspaceTransitionStop)

	kubernetes.setWorkspaceVolumes(source.ID, "source-home", ".cache/template-specific")
	kubernetes.setWorkspaceVolumes(destination.ID, "destination-home")

	// A normal member cannot invoke the administrative volume-copy capability,
	// even on a workspace that they own.
	_, err := memberClient.WorkspaceVolumeCopyVolumes(ctx, destination.ID)
	require.Error(t, err)
	require.Equal(t, 403, coderdtest.SDKError(t, err).StatusCode())

	// Site owner can operate across workspace owners.
	volumes, err := client.WorkspaceVolumeCopyVolumes(ctx, source.ID)
	require.NoError(t, err)
	require.Len(t, volumes, 1)
	require.Equal(t, "home", volumes[0].Key)
	require.Contains(t, volumes[0].ExcludedPaths, ".ssh/id_ed25519_workspace")
	require.Contains(t, volumes[0].ExcludedPaths, ".local/state/coder")
	require.Contains(t, volumes[0].ExcludedPaths, ".cache/template-specific")

	operation, err := client.CreateWorkspaceVolumeCopy(ctx, source.ID, codersdk.CreateWorkspaceVolumeCopyRequest{
		DestinationWorkspaceID: destination.ID,
		Volumes:                []codersdk.WorkspaceVolumeCopySelection{{Key: "home", Overwrite: false}},
	})
	require.NoError(t, err)
	require.Equal(t, source.ID, operation.SourceWorkspaceID)
	require.Equal(t, destination.ID, operation.DestinationWorkspaceID)
	require.False(t, operation.AllowSourceRunning)

	internalCtx := dbauthz.AsWorkspaceVolumeCopy(ctx)
	sourceLock, err := db.GetWorkspaceVolumeCopyLockByWorkspaceID(internalCtx, source.ID)
	require.NoError(t, err)
	require.Equal(t, operation.ID, sourceLock.OperationID)
	destinationLock, err := db.GetWorkspaceVolumeCopyLockByWorkspaceID(internalCtx, destination.ID)
	require.NoError(t, err)
	require.Equal(t, operation.ID, destinationLock.OperationID)

	// The lock is enforced centrally by wsbuilder, so normal lifecycle API calls
	// from either owner cannot race a copy operation.
	_, err = client.CreateWorkspaceBuild(ctx, source.ID, codersdk.CreateWorkspaceBuildRequest{Transition: codersdk.WorkspaceTransitionStart})
	require.Error(t, err)
	require.Equal(t, 409, coderdtest.SDKError(t, err).StatusCode())
	_, err = memberClient.CreateWorkspaceBuild(ctx, destination.ID, codersdk.CreateWorkspaceBuildRequest{Transition: codersdk.WorkspaceTransitionStart})
	require.Error(t, err)
	require.Equal(t, 409, coderdtest.SDKError(t, err).StatusCode())

	kubernetes.completeAllJobs()
	completed := awaitWorkspaceVolumeCopyStatus(t, client, operation.ID, codersdk.WorkspaceVolumeCopyStatusSucceeded)
	require.NotNil(t, completed.CompletedAt)
	_, err = db.GetWorkspaceVolumeCopyLockByWorkspaceID(internalCtx, source.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = db.GetWorkspaceVolumeCopyLockByWorkspaceID(internalCtx, destination.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// Sync again is intentionally a live-copy-only workflow. A successful strict
	// copy must be rejected by the API even though the UI does not offer the action.
	_, err = client.SyncWorkspaceVolumeCopy(ctx, operation.ID)
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, coderdtest.SDKError(t, err).StatusCode())
}

func TestWorkspaceVolumeCopyLiveSourceMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kubernetes := newFakeWorkspaceVolumeCopyKubernetes()
	client := coderdtest.New(t, &coderdtest.Options{
		IncludeProvisionerDaemon: true,
		DeploymentValues: coderdtest.DeploymentValues(t, func(values *codersdk.DeploymentValues) {
			values.WorkspaceVolumeCopyEnabled = serpent.Bool(true)
			values.WorkspaceVolumeCopyNamespace = serpent.String("coder-workspaces")
			values.WorkspaceVolumeCopyImage = serpent.String("ghcr.io/biptec/coder@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		}),
		WorkspaceVolumeCopyKubernetes: kubernetes,
	})
	owner := coderdtest.CreateFirstUser(t, client)
	version := coderdtest.CreateTemplateVersion(t, client, owner.OrganizationID, &echo.Responses{Parse: echo.ParseComplete})
	coderdtest.AwaitTemplateVersionJobCompleted(t, client, version.ID)
	template := coderdtest.CreateTemplate(t, client, owner.OrganizationID, version.ID)

	source := coderdtest.CreateWorkspace(t, client, template.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, source.LatestBuild.ID)
	destination := coderdtest.CreateWorkspace(t, client, template.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, destination.LatestBuild.ID)
	destination = coderdtest.MustTransitionWorkspace(t, client, destination.ID, codersdk.WorkspaceTransitionStart, codersdk.WorkspaceTransitionStop)
	kubernetes.setWorkspaceVolumes(source.ID, "source-home")
	kubernetes.setWorkspaceVolumes(destination.ID, "destination-home")

	// Strict mode rejects a running source.
	_, err := client.CreateWorkspaceVolumeCopy(ctx, source.ID, codersdk.CreateWorkspaceVolumeCopyRequest{
		DestinationWorkspaceID: destination.ID,
		Volumes:                []codersdk.WorkspaceVolumeCopySelection{{Key: "home"}},
	})
	require.Error(t, err)
	require.Equal(t, 409, coderdtest.SDKError(t, err).StatusCode())

	// Explicit live mode allows the same source to remain running, but locks its
	// lifecycle so another actor cannot stop/restart it halfway through the copy.
	operation, err := client.CreateWorkspaceVolumeCopy(ctx, source.ID, codersdk.CreateWorkspaceVolumeCopyRequest{
		DestinationWorkspaceID: destination.ID,
		AllowSourceRunning:     true,
		Volumes:                []codersdk.WorkspaceVolumeCopySelection{{Key: "home", Overwrite: true}},
	})
	require.NoError(t, err)
	require.True(t, operation.AllowSourceRunning)
	_, err = client.CreateWorkspaceBuild(ctx, source.ID, codersdk.CreateWorkspaceBuildRequest{Transition: codersdk.WorkspaceTransitionStop})
	require.Error(t, err)
	require.Equal(t, 409, coderdtest.SDKError(t, err).StatusCode())

	workspace, err := client.Workspace(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, codersdk.WorkspaceStatusRunning, workspace.LatestBuild.Status)

	kubernetes.completeAllJobs()
	_ = awaitWorkspaceVolumeCopyStatus(t, client, operation.ID, codersdk.WorkspaceVolumeCopyStatusSucceeded)

	// Sync again creates a fresh durable operation and rediscovers the current
	// PVC claims, exclusions, and explicit ownership overrides instead of reusing
	// the resolved plan from the first live copy.
	sourceUID, sourceGID := uint32(1100), uint32(1101)
	destinationUID, destinationGID := uint32(2100), uint32(2101)
	kubernetes.setWorkspaceVolume(source.ID, volcopyk8s.Volume{
		Key:           "home",
		DisplayName:   "Home",
		ClaimName:     "source-home-v2",
		ExcludedPaths: []string{".cache/source-v2"},
		OwnerUID:      &sourceUID,
		OwnerGID:      &sourceGID,
	})
	kubernetes.setWorkspaceVolume(destination.ID, volcopyk8s.Volume{
		Key:           "home",
		DisplayName:   "Home",
		ClaimName:     "destination-home-v2",
		ExcludedPaths: []string{".cache/destination-v2"},
		OwnerUID:      &destinationUID,
		OwnerGID:      &destinationGID,
	})

	syncOperation, err := client.SyncWorkspaceVolumeCopy(ctx, operation.ID)
	require.NoError(t, err)
	require.NotNil(t, syncOperation.SyncOf)
	require.Equal(t, operation.ID, *syncOperation.SyncOf)
	require.Equal(t, operation.Volumes, syncOperation.Volumes)

	kubernetes.completeAllJobs()
	_ = awaitWorkspaceVolumeCopyStatus(t, client, syncOperation.ID, codersdk.WorkspaceVolumeCopyStatusSucceeded)
	syncVolumes := kubernetes.volumesForOperation(syncOperation.ID)
	require.Len(t, syncVolumes, 1)
	require.Equal(t, "source-home-v2", syncVolumes[0].SourceClaim)
	require.Equal(t, "destination-home-v2", syncVolumes[0].DestinationClaim)
	require.Equal(t, &sourceUID, syncVolumes[0].SourceOwnerUID)
	require.Equal(t, &sourceGID, syncVolumes[0].SourceOwnerGID)
	require.Equal(t, &destinationUID, syncVolumes[0].DestinationOwnerUID)
	require.Equal(t, &destinationGID, syncVolumes[0].DestinationOwnerGID)
	require.Contains(t, syncVolumes[0].ExcludedPaths, ".cache/source-v2")
	require.Contains(t, syncVolumes[0].ExcludedPaths, ".cache/destination-v2")
}

func TestWorkspaceVolumeCopyDestinationMustBeStopped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kubernetes := newFakeWorkspaceVolumeCopyKubernetes()
	client := coderdtest.New(t, &coderdtest.Options{
		IncludeProvisionerDaemon: true,
		DeploymentValues: coderdtest.DeploymentValues(t, func(values *codersdk.DeploymentValues) {
			values.WorkspaceVolumeCopyEnabled = serpent.Bool(true)
			values.WorkspaceVolumeCopyNamespace = serpent.String("coder-workspaces")
			values.WorkspaceVolumeCopyImage = serpent.String("ghcr.io/biptec/coder@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
		}),
		WorkspaceVolumeCopyKubernetes: kubernetes,
	})
	owner := coderdtest.CreateFirstUser(t, client)
	version := coderdtest.CreateTemplateVersion(t, client, owner.OrganizationID, &echo.Responses{Parse: echo.ParseComplete})
	coderdtest.AwaitTemplateVersionJobCompleted(t, client, version.ID)
	template := coderdtest.CreateTemplate(t, client, owner.OrganizationID, version.ID)
	source := coderdtest.CreateWorkspace(t, client, template.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, source.LatestBuild.ID)
	destination := coderdtest.CreateWorkspace(t, client, template.ID)
	coderdtest.AwaitWorkspaceBuildJobCompleted(t, client, destination.LatestBuild.ID)
	kubernetes.setWorkspaceVolumes(source.ID, "source-home")
	kubernetes.setWorkspaceVolumes(destination.ID, "destination-home")

	_, err := client.CreateWorkspaceVolumeCopy(ctx, source.ID, codersdk.CreateWorkspaceVolumeCopyRequest{
		DestinationWorkspaceID: destination.ID,
		AllowSourceRunning:     true,
		Volumes:                []codersdk.WorkspaceVolumeCopySelection{{Key: "home"}},
	})
	require.Error(t, err)
	require.Equal(t, 409, coderdtest.SDKError(t, err).StatusCode())

	// The rejected operation must not leave a lifecycle lock behind.
	_, err = client.CreateWorkspaceBuild(ctx, destination.ID, codersdk.CreateWorkspaceBuildRequest{Transition: codersdk.WorkspaceTransitionStop})
	require.NoError(t, err)
}

func TestWorkspaceVolumeCopyOrgAdminDoesNotImplicitlyHavePermission(t *testing.T) {
	t.Parallel()

	organizationID := uuid.New()
	orgAdminRole, err := rbac.RoleByName(rbac.ScopedRoleOrgAdmin(organizationID))
	require.NoError(t, err)
	permissions := orgAdminRole.ByOrgID[organizationID.String()].Org
	for _, permission := range permissions {
		if permission.ResourceType == rbac.ResourceWorkspace.Type {
			require.NotEqual(t, policy.ActionWorkspaceVolumeCopy, permission.Action)
		}
	}
}

func awaitWorkspaceVolumeCopyStatus(t *testing.T, client *codersdk.Client, operationID uuid.UUID, want codersdk.WorkspaceVolumeCopyStatus) codersdk.WorkspaceVolumeCopyOperation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
	defer cancel()
	var operation codersdk.WorkspaceVolumeCopyOperation
	testutil.Eventually(ctx, t, func(ctx context.Context) bool {
		var err error
		operation, err = client.WorkspaceVolumeCopyOperation(ctx, operationID)
		return err == nil && operation.Status == want
	}, testutil.IntervalFast)
	return operation
}

var _ volcopyk8s.Kubernetes = (*fakeWorkspaceVolumeCopyKubernetes)(nil)
