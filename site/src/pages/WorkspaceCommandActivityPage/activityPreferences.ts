import type {
	WorkspaceCommandActivitySort,
	WorkspaceCommandActivitySortDirection,
	WorkspaceCommandActivitySource,
	WorkspaceCommandActivityStatus,
} from "#/api/typesGenerated";

export const activityPreferencesStorageKey =
	"coder.workspace-activity.preferences.v1";

export const defaultActivityStatuses: readonly WorkspaceCommandActivityStatus[] =
	["running", "succeeded", "failed", "interrupted"];

export const activityStatusOptions: readonly WorkspaceCommandActivityStatus[] =
	[...defaultActivityStatuses, "idle"];

export const activitySourceOptions: readonly WorkspaceCommandActivitySource[] =
	[
		"mcp",
		"ssh",
		"reconnecting_pty",
		"vscode",
		"jetbrains",
		"chat",
		"agentproc",
	];

export const activityPageSizeOptions = [25, 50, 100, 250, 500] as const;

const activitySortOptions: readonly WorkspaceCommandActivitySort[] = [
	"id",
	"status",
	"started",
	"duration",
	"tool",
	"source",
	"command",
	"exit",
];

export type WorkspaceActivityPreferences = {
	search: string;
	statuses: WorkspaceCommandActivityStatus[];
	// null means "all tools", including tools added in future releases.
	tools: string[] | null;
	source: "all" | WorkspaceCommandActivitySource;
	startedAfter: string;
	startedBefore: string;
	durationMin: string;
	durationMax: string;
	sortBy: WorkspaceCommandActivitySort;
	sortDirection: WorkspaceCommandActivitySortDirection;
	pageSize: number;
};

export const defaultWorkspaceActivityPreferences =
	(): WorkspaceActivityPreferences => ({
		search: "",
		statuses: [...defaultActivityStatuses],
		tools: null,
		source: "all",
		startedAfter: "",
		startedBefore: "",
		durationMin: "",
		durationMax: "",
		sortBy: "started",
		sortDirection: "desc",
		pageSize: 50,
	});

type StoredWorkspaceActivityPreferences = WorkspaceActivityPreferences & {
	version: 1;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
	typeof value === "object" && value !== null && !Array.isArray(value);

const stringValue = (value: unknown, fallback: string): string =>
	typeof value === "string" ? value : fallback;

const uniqueStrings = (value: unknown): string[] | undefined => {
	if (
		!Array.isArray(value) ||
		!value.every((item) => typeof item === "string")
	) {
		return undefined;
	}
	return [...new Set(value.map((item) => item.trim()).filter(Boolean))];
};

export const parseWorkspaceActivityPreferences = (
	value: unknown,
): WorkspaceActivityPreferences => {
	const defaults = defaultWorkspaceActivityPreferences();
	if (!isRecord(value) || value.version !== 1) return defaults;

	const statuses = Array.isArray(value.statuses)
		? value.statuses.filter(
				(status): status is WorkspaceCommandActivityStatus =>
					typeof status === "string" &&
					activityStatusOptions.includes(
						status as WorkspaceCommandActivityStatus,
					),
			)
		: defaults.statuses;
	const tools = value.tools === null ? null : uniqueStrings(value.tools);
	const source =
		typeof value.source === "string" &&
		(value.source === "all" ||
			activitySourceOptions.includes(
				value.source as WorkspaceCommandActivitySource,
			))
			? (value.source as WorkspaceActivityPreferences["source"])
			: defaults.source;
	const sortBy =
		typeof value.sortBy === "string" &&
		activitySortOptions.includes(value.sortBy as WorkspaceCommandActivitySort)
			? (value.sortBy as WorkspaceCommandActivitySort)
			: defaults.sortBy;
	const sortDirection =
		value.sortDirection === "asc" || value.sortDirection === "desc"
			? value.sortDirection
			: defaults.sortDirection;
	const pageSize =
		typeof value.pageSize === "number" &&
		activityPageSizeOptions.includes(
			value.pageSize as (typeof activityPageSizeOptions)[number],
		)
			? value.pageSize
			: defaults.pageSize;

	return {
		search: stringValue(value.search, defaults.search),
		statuses: [...new Set(statuses)],
		tools: tools ?? defaults.tools,
		source,
		startedAfter: stringValue(value.startedAfter, defaults.startedAfter),
		startedBefore: stringValue(value.startedBefore, defaults.startedBefore),
		durationMin: stringValue(value.durationMin, defaults.durationMin),
		durationMax: stringValue(value.durationMax, defaults.durationMax),
		sortBy,
		sortDirection,
		pageSize,
	};
};

export const loadWorkspaceActivityPreferences =
	(): WorkspaceActivityPreferences => {
		if (typeof window === "undefined")
			return defaultWorkspaceActivityPreferences();
		try {
			const raw = window.localStorage.getItem(activityPreferencesStorageKey);
			return raw
				? parseWorkspaceActivityPreferences(JSON.parse(raw))
				: defaultWorkspaceActivityPreferences();
		} catch {
			return defaultWorkspaceActivityPreferences();
		}
	};

export const saveWorkspaceActivityPreferences = (
	preferences: WorkspaceActivityPreferences,
): void => {
	if (typeof window === "undefined") return;
	const stored: StoredWorkspaceActivityPreferences = {
		version: 1,
		...preferences,
	};
	try {
		window.localStorage.setItem(
			activityPreferencesStorageKey,
			JSON.stringify(stored),
		);
	} catch {
		// Browser storage can be unavailable (private mode, quota, policy). The
		// activity page must remain fully functional without persistence.
	}
};
