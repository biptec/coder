package license_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/rbac"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/enterprise/coderd/coderdenttest"
	"github.com/coder/coder/v2/enterprise/coderd/license"
)

// TestCountWorkspaceCapableUsers verifies permission-based license seat
// counting: only users the RBAC engine authorizes to create workspaces
// consume seats, so members without workspace-create ("gateway accounts")
// are excluded.
//
// The subtests toggle the global builtin roles via ReloadBuiltinRoles, so
// they must run serially.
//
//nolint:tparallel,paralleltest
func TestCountWorkspaceCapableUsers(t *testing.T) {
	ctx := context.Background()
	authorizer := rbac.NewCachingAuthorizer(prometheus.NewRegistry())

	activeUser := func(t *testing.T, db database.Store, seed database.User) database.User {
		seed.Status = database.UserStatusActive
		return dbgen.User(t, db, seed)
	}
	member := func(t *testing.T, db database.Store, orgID uuid.UUID, user database.User, roles ...string) {
		dbgen.OrganizationMember(t, db, database.OrganizationMember{
			OrganizationID: orgID,
			UserID:         user.ID,
			Roles:          roles,
		})
	}
	emptyDefaultRoles := func(t *testing.T, db database.Store, org database.Organization) {
		_, err := db.UpdateOrganization(ctx, database.UpdateOrganizationParams{
			ID:                    org.ID,
			UpdatedAt:             dbtime.Now(),
			Name:                  org.Name,
			DisplayName:           org.DisplayName,
			Description:           org.Description,
			Icon:                  org.Icon,
			DefaultOrgMemberRoles: []string{},
		})
		require.NoError(t, err)
	}

	t.Run("ElevationBundledParity", func(t *testing.T) {
		// MinimumImplicitMember off (default): organization-member bundles
		// the workspace-ops elevation, so every active org member counts
		// and the permission-based count matches the legacy count except
		// for zero-org plain members.
		rbac.ReloadBuiltinRoles(nil)
		t.Cleanup(func() { rbac.ReloadBuiltinRoles(nil) })

		db, _ := dbtestutil.NewDB(t)
		org := dbgen.Organization(t, db, database.Organization{})

		plainMember := activeUser(t, db, database.User{})
		member(t, db, org.ID, plainMember)

		orgAdmin := activeUser(t, db, database.User{})
		member(t, db, org.ID, orgAdmin, rbac.RoleOrgAdmin())

		owner := activeUser(t, db, database.User{RBACRoles: []string{rbac.RoleOwner().Name}})
		member(t, db, org.ID, owner)

		// Counts under legacy, not under permission-based: no org, no
		// workspace-create anywhere.
		activeUser(t, db, database.User{})

		// Counts under both: the owner site role grants workspace-create
		// in any organization, independent of membership.
		activeUser(t, db, database.User{RBACRoles: []string{rbac.RoleOwner().Name}})

		// Never counted: not active.
		suspended := dbgen.User(t, db, database.User{Status: database.UserStatusSuspended})
		member(t, db, org.ID, suspended)
		dormant := dbgen.User(t, db, database.User{Status: database.UserStatusDormant})
		member(t, db, org.ID, dormant)

		// Never counted: service accounts are excluded from seat counts.
		sa := activeUser(t, db, database.User{IsServiceAccount: true})
		member(t, db, org.ID, sa)

		legacy, err := db.GetActiveUserCount(ctx, false)
		require.NoError(t, err)
		require.Equal(t, int64(5), legacy)

		count, err := license.CountWorkspaceCapableUsers(ctx, db, authorizer)
		require.NoError(t, err)
		require.Equal(t, int64(4), count, "zero-org plain member must not count")
	})

	t.Run("MinimumImplicitMember", func(t *testing.T) {
		// MinimumImplicitMember on: organization-member carries only the
		// floor. Workspace-create flows exclusively through the
		// organization-workspace-access role, granted explicitly or via
		// default_org_member_roles.
		rbac.ReloadBuiltinRoles(&rbac.RoleOptions{MinimumImplicitMember: true})
		t.Cleanup(func() { rbac.ReloadBuiltinRoles(nil) })

		db, _ := dbtestutil.NewDB(t)
		org := dbgen.Organization(t, db, database.Organization{})
		emptyDefaultRoles(t, db, org)

		// Gateway account: floor only, no workspace-create. Not counted.
		gateway := activeUser(t, db, database.User{})
		member(t, db, org.ID, gateway)

		// Explicit organization-workspace-access grant. Counted.
		wsUser := activeUser(t, db, database.User{})
		member(t, db, org.ID, wsUser, rbac.RoleOrgWorkspaceAccess())

		// The creation ban negates workspace-create even when the
		// workspace-access role is present. Not counted.
		banned := activeUser(t, db, database.User{})
		member(t, db, org.ID, banned, rbac.RoleOrgWorkspaceAccess(), rbac.RoleOrgWorkspaceCreationBan())

		// Org admins retain workspace-create. Counted.
		orgAdmin := activeUser(t, db, database.User{})
		member(t, db, org.ID, orgAdmin, rbac.RoleOrgAdmin())

		// Owners retain workspace-create. Counted.
		owner := activeUser(t, db, database.User{RBACRoles: []string{rbac.RoleOwner().Name}})
		member(t, db, org.ID, owner)

		// Members of an org that keeps organization-workspace-access in
		// default_org_member_roles inherit workspace-create. Counted.
		defaultOrg := dbgen.Organization(t, db, database.Organization{})
		defaultMember := activeUser(t, db, database.User{})
		member(t, db, defaultOrg.ID, defaultMember)

		count, err := license.CountWorkspaceCapableUsers(ctx, db, authorizer)
		require.NoError(t, err)
		require.Equal(t, int64(4), count)
	})

	t.Run("CustomOrgRole", func(t *testing.T) {
		rbac.ReloadBuiltinRoles(&rbac.RoleOptions{MinimumImplicitMember: true})
		t.Cleanup(func() { rbac.ReloadBuiltinRoles(nil) })

		db, _ := dbtestutil.NewDB(t)
		org := dbgen.Organization(t, db, database.Organization{})
		emptyDefaultRoles(t, db, org)

		creatorRole, err := db.InsertCustomRole(ctx, database.InsertCustomRoleParams{
			Name:           "workspace-creator",
			DisplayName:    "Workspace Creator",
			OrganizationID: uuid.NullUUID{UUID: org.ID, Valid: true},
			OrgPermissions: []database.CustomRolePermission{{
				ResourceType: rbac.ResourceWorkspace.Type,
				Action:       policy.ActionCreate,
			}},
		})
		require.NoError(t, err)

		auditRole, err := db.InsertCustomRole(ctx, database.InsertCustomRoleParams{
			Name:           "org-reader",
			DisplayName:    "Org Reader",
			OrganizationID: uuid.NullUUID{UUID: org.ID, Valid: true},
			OrgPermissions: []database.CustomRolePermission{{
				ResourceType: rbac.ResourceOrganization.Type,
				Action:       policy.ActionRead,
			}},
		})
		require.NoError(t, err)

		// Custom org role with workspace-create. Counted.
		creator := activeUser(t, db, database.User{})
		member(t, db, org.ID, creator, creatorRole.Name)

		// Custom org role without workspace-create. Not counted.
		reader := activeUser(t, db, database.User{})
		member(t, db, org.ID, reader, auditRole.Name)

		count, err := license.CountWorkspaceCapableUsers(ctx, db, authorizer)
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
	})

	t.Run("EntitlementsExperimentGate", func(t *testing.T) {
		rbac.ReloadBuiltinRoles(&rbac.RoleOptions{MinimumImplicitMember: true})
		t.Cleanup(func() { rbac.ReloadBuiltinRoles(nil) })

		db, _ := dbtestutil.NewDB(t)
		org := dbgen.Organization(t, db, database.Organization{})
		emptyDefaultRoles(t, db, org)

		gateway := activeUser(t, db, database.User{})
		member(t, db, org.ID, gateway)
		wsUser := activeUser(t, db, database.User{})
		member(t, db, org.ID, wsUser, rbac.RoleOrgWorkspaceAccess())

		enablements := map[codersdk.FeatureName]bool{}

		// Experiment off: legacy count includes the gateway account.
		entitlements, err := license.Entitlements(ctx, db, 1, 1, coderdenttest.Keys, enablements, authorizer, nil)
		require.NoError(t, err)
		require.Equal(t, int64(2), *entitlements.Features[codersdk.FeatureUserLimit].Actual)

		// Experiment on: only the workspace-capable user counts.
		entitlements, err = license.Entitlements(ctx, db, 1, 1, coderdenttest.Keys, enablements, authorizer,
			codersdk.Experiments{codersdk.ExperimentPermissionBasedLicensing})
		require.NoError(t, err)
		require.Equal(t, int64(1), *entitlements.Features[codersdk.FeatureUserLimit].Actual)

		// Experiment on without an authorizer: fall back to the legacy
		// count instead of failing.
		entitlements, err = license.Entitlements(ctx, db, 1, 1, coderdenttest.Keys, enablements, nil,
			codersdk.Experiments{codersdk.ExperimentPermissionBasedLicensing})
		require.NoError(t, err)
		require.Equal(t, int64(2), *entitlements.Features[codersdk.FeatureUserLimit].Actual)
	})
}
