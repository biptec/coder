import type {
	WorkspaceCommandActivitySort,
	WorkspaceCommandActivitySortDirection,
	WorkspaceCommandActivitySource,
	WorkspaceCommandActivityStatus,
} from "#/api/typesGenerated";

export const activityPreferencesStorageKey =
	"coder.activity-history.preferences.v2";
const legacyActivityPreferencesStorageKey =
	"coder.workspace-activity.preferences.v1";

export const defaultRealActivityStatuses = [
	"running",
	"succeeded",
	"failed",
	"interrupted",
] as const satisfies readonly WorkspaceCommandActivityStatus[];

export type ActivityDisplayStatus = WorkspaceCommandActivityStatus | "idle";
export const activityStatusOptions: readonly ActivityDisplayStatus[] = [
	...defaultRealActivityStatuses,
	"idle",
];

export const activitySourceOptions = [
	"mcp",
	"ssh",
	"reconnecting_pty",
	"chat",
] as const satisfies readonly WorkspaceCommandActivitySource[];
export type ActivityVisibleSource = (typeof activitySourceOptions)[number];

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
	id: string;
	input: string;
	statuses: ActivityDisplayStatus[];
	// null means "all tools", including tools added in future releases.
	tools: string[] | null;
	// Activity history intentionally exposes only the supported user-facing
	// sources. Internal compatibility/editor sources never participate in this UI.
	sources: ActivityVisibleSource[];
	startedAfter: string;
	startedBefore: string;
	durationMin: string;
	durationMax: string;
	exitCode: string;
	sortBy: WorkspaceCommandActivitySort;
	sortDirection: WorkspaceCommandActivitySortDirection;
	pageSize: number;
};

export const defaultWorkspaceActivityPreferences =
	(): WorkspaceActivityPreferences => ({
		id: "",
		input: "",
		statuses: [...defaultRealActivityStatuses],
		tools: null,
		sources: [...activitySourceOptions],
		startedAfter: "",
		startedBefore: "",
		durationMin: "",
		durationMax: "",
		exitCode: "",
		sortBy: "started",
		sortDirection: "desc",
		pageSize: 50,
	});

type StoredWorkspaceActivityPreferences = WorkspaceActivityPreferences & {
	version: 2;
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

const parseStatuses = (
	value: unknown,
	fallback: readonly ActivityDisplayStatus[],
): ActivityDisplayStatus[] => {
	if (!Array.isArray(value)) return [...fallback];
	return [
		...new Set(
			value.filter(
				(status): status is ActivityDisplayStatus =>
					typeof status === "string" &&
					activityStatusOptions.includes(status as ActivityDisplayStatus),
			),
		),
	];
};

const parseSources = (
	value: unknown,
	fallback: ActivityVisibleSource[],
): ActivityVisibleSource[] => {
	if (value === null) return [...fallback];
	const values = uniqueStrings(value);
	if (!values) return fallback;
	return values.filter((source): source is ActivityVisibleSource =>
		activitySourceOptions.includes(source as ActivityVisibleSource),
	);
};

const parseCommon = (
	value: Record<string, unknown>,
	defaults: WorkspaceActivityPreferences,
): Pick<
	WorkspaceActivityPreferences,
	"tools" | "sortBy" | "sortDirection" | "pageSize"
> => {
	const tools = value.tools === null ? null : uniqueStrings(value.tools);
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
	return { tools: tools ?? defaults.tools, sortBy, sortDirection, pageSize };
};

export const parseWorkspaceActivityPreferences = (
	value: unknown,
): WorkspaceActivityPreferences => {
	const defaults = defaultWorkspaceActivityPreferences();
	if (!isRecord(value)) return defaults;

	const common = parseCommon(value, defaults);
	if (value.version === 2) {
		return {
			id: stringValue(value.id, defaults.id),
			input: stringValue(value.input, defaults.input),
			statuses: parseStatuses(value.statuses, defaults.statuses),
			tools: common.tools,
			sources: parseSources(value.sources, defaults.sources),
			startedAfter: stringValue(value.startedAfter, defaults.startedAfter),
			startedBefore: stringValue(value.startedBefore, defaults.startedBefore),
			durationMin: stringValue(value.durationMin, defaults.durationMin),
			durationMax: stringValue(value.durationMax, defaults.durationMax),
			exitCode: stringValue(value.exitCode, defaults.exitCode),
			sortBy: common.sortBy,
			sortDirection: common.sortDirection,
			pageSize: common.pageSize,
		};
	}

	// Migrate the first browser-only preference format used by the initial
	// Activity page. Unsupported legacy sources intentionally fall back to All.
	if (value.version === 1) {
		const legacySource =
			typeof value.source === "string" &&
			activitySourceOptions.includes(value.source as ActivityVisibleSource)
				? [value.source as ActivityVisibleSource]
				: [...activitySourceOptions];
		return {
			id: "",
			input: stringValue(value.search, defaults.input),
			statuses: parseStatuses(value.statuses, defaults.statuses),
			tools: common.tools,
			sources: legacySource,
			startedAfter: stringValue(value.startedAfter, defaults.startedAfter),
			startedBefore: stringValue(value.startedBefore, defaults.startedBefore),
			durationMin: stringValue(value.durationMin, defaults.durationMin),
			durationMax: stringValue(value.durationMax, defaults.durationMax),
			exitCode: "",
			sortBy: common.sortBy,
			sortDirection: common.sortDirection,
			pageSize: common.pageSize,
		};
	}

	return defaults;
};

export const loadWorkspaceActivityPreferences =
	(): WorkspaceActivityPreferences => {
		if (typeof window === "undefined")
			return defaultWorkspaceActivityPreferences();
		try {
			const current = window.localStorage.getItem(
				activityPreferencesStorageKey,
			);
			if (current)
				return parseWorkspaceActivityPreferences(JSON.parse(current));
			const legacy = window.localStorage.getItem(
				legacyActivityPreferencesStorageKey,
			);
			return legacy
				? parseWorkspaceActivityPreferences(JSON.parse(legacy))
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
		version: 2,
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
