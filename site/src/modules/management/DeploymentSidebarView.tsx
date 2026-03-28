import { ArrowUpRightIcon } from "lucide-react";
import type { FC } from "react";
import {
	Sidebar as BaseSidebar,
	SettingsSidebarNavItem as SidebarNavItem,
} from "#/components/Sidebar/Sidebar";
import type { Permissions } from "#/modules/permissions";
>>>>>>> 244569afed (feat(site): promote OAuth2 Applications sidebar item to GA visibility)

interface DeploymentSidebarViewProps {
	/** Site-wide permissions. */
	permissions: Permissions;
	showOrganizations: boolean;
	hasPremiumLicense: boolean;
}

/**
 * Displays navigation for deployment settings.  If active, highlight the main
 * menu heading.
 */
export const DeploymentSidebarView: FC<DeploymentSidebarViewProps> = ({
	permissions,
	showOrganizations,
	hasPremiumLicense,
}) => {
	return (
		<BaseSidebar>
			<div className="flex flex-col gap-1">
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/overview">Overview</SidebarNavItem>
				)}
				{permissions.viewAllLicenses && (
					<SidebarNavItem href="/deployment/licenses">Licenses</SidebarNavItem>
				)}
				{permissions.editDeploymentConfig && (
					<SidebarNavItem href="/deployment/appearance">
						Appearance
					</SidebarNavItem>
				)}
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/userauth">
						User Authentication
					</SidebarNavItem>
				)}
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/external-auth">
						External Authentication
					</SidebarNavItem>
				)}
<<<<<<< HEAD
				{permissions.viewDeploymentConfig &&
					(experiments.includes("oauth2") ||
						getPrereleaseFlag(buildInfo) === "devel") && (
						<SidebarNavItem href="/deployment/oauth2-provider/apps">
							OAuth2 Applications
						</SidebarNavItem>
					)}
=======
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/oauth2-provider/apps">
						OAuth2 Applications
					</SidebarNavItem>
				)}
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/network">Network</SidebarNavItem>
				)}
				{permissions.readWorkspaceProxies && (
					<SidebarNavItem href="/deployment/workspace-proxies">
						Workspace Proxies
					</SidebarNavItem>
				)}
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/security">Security</SidebarNavItem>
				)}
				{permissions.viewDeploymentConfig && (
					<SidebarNavItem href="/deployment/observability">
						Observability
					</SidebarNavItem>
				)}

				{permissions.viewAllUsers && (
					<SidebarNavItem href="/deployment/users">Users</SidebarNavItem>
				)}
				{permissions.viewAnyGroup && (
					<SidebarNavItem href="/deployment/groups">
						<div className="flex flex-row items-center gap-1">
							Groups {showOrganizations && <ArrowUpRightIcon size={16} />}
						</div>
					</SidebarNavItem>
				)}
				{permissions.viewOrganizationIDPSyncSettings && (
					<SidebarNavItem href="/deployment/idp-org-sync">
						IdP Organization Sync
					</SidebarNavItem>
				)}
				{permissions.viewNotificationTemplate && (
					<SidebarNavItem href="/deployment/notifications">
						<div className="flex flex-row items-center gap-2">
							<span>Notifications</span>
						</div>
					</SidebarNavItem>
				)}
				{!hasPremiumLicense && (
					<SidebarNavItem href="/deployment/premium">Premium</SidebarNavItem>
				)}
			</div>
		</BaseSidebar>
	);
};
