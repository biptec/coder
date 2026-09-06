import type { Meta, StoryObj } from "@storybook/react-vite";
import type { FC } from "react";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	workspaceByIdKey,
	workspaceByOwnerAndNameKey,
	workspacesKey,
	workspaceVolumeCopyVolumes,
} from "#/api/queries/workspaces";
import type {
	Workspace,
	WorkspaceVolumeCopyVolume,
} from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import type { WorkspacePermissions } from "#/modules/workspaces/permissions";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	MockStoppedWorkspace,
	MockWorkspace,
} from "#/testHelpers/entities";
import WorkspaceVolumeCopyPage from "./WorkspaceVolumeCopyPage";

const sourceStopped: Workspace = {
	...MockStoppedWorkspace,
	id: "source-workspace",
	name: "network-idm",
	owner_name: "coder-admin",
};

const sourceRunning: Workspace = {
	...MockWorkspace,
	id: sourceStopped.id,
	name: sourceStopped.name,
	owner_name: sourceStopped.owner_name,
};

const destination: Workspace = {
	...MockStoppedWorkspace,
	id: "destination-workspace",
	name: "identity-management",
	owner_name: "developer",
};

const homeVolume: WorkspaceVolumeCopyVolume = {
	key: "home",
	display_name: "Home",
	mount_path: "/home/coder",
	capacity: "20Gi",
	excluded_paths: [
		".ssh/id_ed25519_workspace",
		".ssh/id_ed25519_workspace.pub",
		".ssh/config.d/00-coder-workspace-github.conf",
		".local/state/coder-command-activity",
		".local/state/coder",
		".developer-workspace-seeded",
	],
};

const volumeCopyPermissions: WorkspacePermissions = {
	readWorkspace: true,
	shareWorkspace: true,
	updateWorkspace: true,
	volumeCopyWorkspace: true,
	updateWorkspaceVersion: true,
	deleteFailedWorkspace: true,
};

const withVolumeCopyDashboard = (Story: FC) => (
	<DashboardContext.Provider
		value={{
			entitlements: MockEntitlements,
			experiments: [],
			appearance: {
				...MockAppearanceConfig,
				workspace_volume_copy_enabled: true,
			},
			buildInfo: MockBuildInfo,
			organizations: [MockDefaultOrganization],
			showOrganizations: false,
			canViewOrganizationSettings: false,
		}}
	>
		<Story />
	</DashboardContext.Provider>
);

const meta = {
	title: "pages/WorkspaceVolumeCopyPage",
	component: WorkspaceVolumeCopyPage,
	decorators: [withVolumeCopyDashboard],
	parameters: {
		layout: "fullscreen",
	},
} satisfies Meta<typeof WorkspaceVolumeCopyPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const workspaceQueries = (source: Workspace) => [
	{
		key: workspaceByOwnerAndNameKey(source.owner_name, source.name),
		data: source,
	},
	{
		key: ["workspaces", source.id, "permissions"],
		data: volumeCopyPermissions,
	},
	{
		key: workspaceVolumeCopyVolumes(source.id).queryKey,
		data: [homeVolume],
	},
	{
		key: workspacesKey({ limit: 25, offset: 0, q: "" }),
		data: { workspaces: [source, destination], count: 2 },
	},
	{
		key: workspaceByIdKey(destination.id),
		data: destination,
	},
	{
		key: ["workspaces", destination.id, "permissions"],
		data: volumeCopyPermissions,
	},
	{
		key: workspaceVolumeCopyVolumes(destination.id).queryKey,
		data: [homeVolume],
	},
];

const routerParameters = (source: Workspace) =>
	reactRouterParameters({
		location: {
			pathParams: {
				username: `@${source.owner_name}`,
				workspace: source.name,
			},
		},
		routing: {
			path: "/:username/:workspace/volume-copy",
		},
	});

export const StoppedSource: Story = {
	parameters: {
		reactRouter: routerParameters(sourceStopped),
		queries: workspaceQueries(sourceStopped),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("button", { name: "Select destination workspace" }),
		);
		await userEvent.click(
			screen.getByText("developer/identity-management", { exact: true }),
		);

		await waitFor(() => {
			expect(canvas.getByText("Home", { exact: true })).toBeVisible();
			expect(
				canvas.getByRole("button", { name: "Copy volumes" }),
			).toBeEnabled();
		});

		await userEvent.click(
			canvas.getByRole("checkbox", { name: "Overwrite existing files" }),
		);
		expect(
			canvas.getByRole("checkbox", { name: "Overwrite existing files" }),
		).toBeChecked();
	},
};

export const RunningSource: Story = {
	parameters: {
		reactRouter: routerParameters(sourceRunning),
		queries: workspaceQueries(sourceRunning),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("button", { name: "Select destination workspace" }),
		);
		await userEvent.click(
			screen.getByText("developer/identity-management", { exact: true }),
		);

		expect(canvas.getByRole("button", { name: "Copy volumes" })).toBeDisabled();
		await userEvent.click(
			canvas.getByRole("checkbox", {
				name: "Allow copying while source workspace is running",
			}),
		);

		await waitFor(() => {
			expect(canvas.getByText("Source workspace is running.")).toBeVisible();
			expect(
				canvas.getByRole("button", { name: "Copy volumes" }),
			).toBeEnabled();
		});
	},
};
