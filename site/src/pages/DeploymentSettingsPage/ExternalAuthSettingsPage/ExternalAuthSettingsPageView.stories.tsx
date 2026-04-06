import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ExternalAuthProviderEntry } from "#/api/typesGenerated";
import { ExternalAuthSettingsPageView } from "./ExternalAuthSettingsPageView";

const mockGitHubProvider: ExternalAuthProviderEntry = {
	id: "github-provider-1",
	provider_id: "github",
	type: "GitHub",
	display_name: "GitHub",
	display_icon: "/icon/github.svg",
	client_id: "Iv1.abcdef1234567890",
	has_client_secret: true,
	auth_url: "https://github.com/login/oauth/authorize",
	token_url: "https://github.com/login/oauth/access_token",
	validate_url: "",
	revoke_url: "",
	device_code_url: "",
	scopes: ["repo", "user"],
	extra_token_keys: [],
	no_refresh: false,
	device_flow: false,
	regex: "",
	app_install_url: "",
	app_installations_url: "",
	code_challenge_methods: [],
	source: "env",
	created_at: "2024-01-01T00:00:00Z",
	updated_at: "2024-01-01T00:00:00Z",
};

const mockGitLabProvider: ExternalAuthProviderEntry = {
	id: "gitlab-provider-1",
	provider_id: "gitlab",
	type: "GitLab",
	display_name: "GitLab",
	display_icon: "/icon/gitlab.svg",
	client_id: "app-1234567890abcdef",
	has_client_secret: true,
	auth_url: "https://gitlab.com/oauth/authorize",
	token_url: "https://gitlab.com/oauth/token",
	validate_url: "",
	revoke_url: "",
	device_code_url: "",
	scopes: ["read_user", "api"],
	extra_token_keys: [],
	no_refresh: false,
	device_flow: false,
	regex: "",
	app_install_url: "",
	app_installations_url: "",
	code_challenge_methods: [],
	source: "database",
	created_at: "2024-02-01T00:00:00Z",
	updated_at: "2024-02-01T00:00:00Z",
};

const mockBitbucketProvider: ExternalAuthProviderEntry = {
	id: "bitbucket-provider-1",
	provider_id: "bitbucket",
	type: "BitBucket",
	display_name: "",
	display_icon: "",
	client_id: "bb-client-9876543210",
	has_client_secret: true,
	auth_url: "https://bitbucket.org/site/oauth2/authorize",
	token_url: "https://bitbucket.org/site/oauth2/access_token",
	validate_url: "",
	revoke_url: "",
	device_code_url: "",
	scopes: ["repository"],
	extra_token_keys: [],
	no_refresh: false,
	device_flow: false,
	regex: "",
	app_install_url: "",
	app_installations_url: "",
	code_challenge_methods: [],
	source: "env",
	created_at: "2024-03-01T00:00:00Z",
	updated_at: "2024-03-01T00:00:00Z",
};

const meta: Meta<typeof ExternalAuthSettingsPageView> = {
	title: "pages/DeploymentSettingsPage/ExternalAuthSettingsPageView",
	component: ExternalAuthSettingsPageView,
	args: {
		providers: [mockGitHubProvider, mockGitLabProvider],
		isLoading: false,
		error: undefined,
		canCreateProvider: true,
		onDeleteProvider: async () => {},
		deleteProviderLoading: false,
	},
};

export default meta;
type Story = StoryObj<typeof ExternalAuthSettingsPageView>;

export const Default: Story = {};

export const Empty: Story = {
	args: {
		providers: [],
	},
};

export const Loading: Story = {
	args: {
		providers: undefined,
		isLoading: true,
	},
};

export const AllEnvSourced: Story = {
	args: {
		providers: [mockGitHubProvider, mockBitbucketProvider],
	},
};
