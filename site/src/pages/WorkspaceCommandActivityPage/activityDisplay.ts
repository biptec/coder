import type {
	WorkspaceCommandActivity,
	WorkspaceCommandActivitySort,
	WorkspaceCommandActivitySortDirection,
	WorkspaceMCPRequestActivity,
} from "#/api/typesGenerated";

export type ActivityDisplayRow =
	| { type: "activity"; activity: WorkspaceCommandActivity }
	| {
			type: "idle";
			key: string;
			startedAt: string;
			finishedAt?: string;
			current: boolean;
	  };

export const activityInput = (activity: WorkspaceCommandActivity): string => {
	if (activity.command?.trim()) return activity.command.trim();
	return activity.argv?.join(" ").trim() ?? "";
};

export const activitySourceLabel = (source: string): string => {
	switch (source) {
		case "mcp":
			return "MCP";
		case "ssh":
			return "SSH";
		case "reconnecting_pty":
			return "Web terminal";
		case "chat":
			return "Agent Chat";
		default:
			// Internal/unsupported compatibility sources (agentproc, VS Code,
			// JetBrains) are deliberately not exposed as product concepts here.
			return "—";
	}
};

type BusyInterval = {
	startedAt: number;
	finishedAt: number;
};

const timestamp = (value: string | undefined): number | undefined => {
	if (!value) return undefined;
	const parsed = new Date(value).getTime();
	return Number.isFinite(parsed) ? parsed : undefined;
};

const mergeBusyIntervals = (
	requests: readonly WorkspaceMCPRequestActivity[],
): BusyInterval[] => {
	const intervals = requests
		.map((request) => {
			const startedAt = timestamp(request.started_at);
			if (startedAt === undefined) return undefined;
			const finishedAt =
				timestamp(request.finished_at) ?? Number.POSITIVE_INFINITY;
			return { startedAt, finishedAt };
		})
		.filter((interval): interval is BusyInterval => interval !== undefined)
		.sort((a, b) => a.startedAt - b.startedAt || a.finishedAt - b.finishedAt);

	const merged: BusyInterval[] = [];
	for (const interval of intervals) {
		const previous = merged.at(-1);
		if (!previous || interval.startedAt > previous.finishedAt) {
			merged.push({ ...interval });
			continue;
		}
		previous.finishedAt = Math.max(previous.finishedAt, interval.finishedAt);
	}
	return merged;
};

const currentIdleRow = (
	requests: readonly WorkspaceMCPRequestActivity[],
): ActivityDisplayRow | undefined => {
	if (requests.some((request) => !request.finished_at)) return undefined;
	let latestFinishedAt: number | undefined;
	for (const request of requests) {
		const finishedAt = timestamp(request.finished_at);
		if (
			finishedAt !== undefined &&
			(latestFinishedAt === undefined || finishedAt > latestFinishedAt)
		) {
			latestFinishedAt = finishedAt;
		}
	}
	if (latestFinishedAt === undefined) return undefined;
	const startedAt = new Date(latestFinishedAt).toISOString();
	return {
		type: "idle",
		key: `idle:current:${startedAt}`,
		startedAt,
		current: true,
	};
};

const historicalIdleRows = (
	requests: readonly WorkspaceMCPRequestActivity[],
	activity: readonly WorkspaceCommandActivity[],
): ActivityDisplayRow[] => {
	if (activity.length === 0) return [];

	let rangeStart = Number.POSITIVE_INFINITY;
	let rangeEnd = Number.NEGATIVE_INFINITY;
	for (const item of activity) {
		const startedAt = timestamp(item.started_at);
		if (startedAt === undefined) continue;
		rangeStart = Math.min(rangeStart, startedAt);
		const finishedAt = timestamp(item.finished_at) ?? startedAt;
		rangeEnd = Math.max(rangeEnd, finishedAt);
	}
	if (!Number.isFinite(rangeStart) || !Number.isFinite(rangeEnd)) return [];

	const overlapping: WorkspaceMCPRequestActivity[] = [];
	let previous: WorkspaceMCPRequestActivity | undefined;
	let previousFinishedAt = Number.NEGATIVE_INFINITY;
	for (const request of requests) {
		const startedAt = timestamp(request.started_at);
		if (startedAt === undefined) continue;
		const finishedAt = timestamp(request.finished_at);
		if (
			startedAt <= rangeEnd &&
			(finishedAt === undefined || finishedAt >= rangeStart)
		) {
			overlapping.push(request);
			continue;
		}
		if (
			finishedAt !== undefined &&
			finishedAt < rangeStart &&
			finishedAt > previousFinishedAt
		) {
			previous = request;
			previousFinishedAt = finishedAt;
		}
	}
	if (previous) overlapping.push(previous);

	const busy = mergeBusyIntervals(overlapping);
	const rows: ActivityDisplayRow[] = [];
	for (let index = 0; index + 1 < busy.length; index++) {
		const earlier = busy[index];
		const later = busy[index + 1];
		if (!Number.isFinite(earlier.finishedAt)) continue;
		if (earlier.finishedAt >= later.startedAt) continue;
		// Only render historical gaps that touch the visible chronological page.
		if (later.startedAt < rangeStart || earlier.finishedAt > rangeEnd) continue;
		const startedAt = new Date(earlier.finishedAt).toISOString();
		const finishedAt = new Date(later.startedAt).toISOString();
		rows.push({
			type: "idle",
			key: `idle:${startedAt}:${finishedAt}`,
			startedAt,
			finishedAt,
			current: false,
		});
	}
	return rows;
};

const rowStartedAt = (row: ActivityDisplayRow): number =>
	row.type === "activity"
		? (timestamp(row.activity.started_at) ?? 0)
		: (timestamp(row.startedAt) ?? 0);

const chronologicalRows = (
	rows: ActivityDisplayRow[],
	direction: WorkspaceCommandActivitySortDirection,
): ActivityDisplayRow[] =>
	rows.sort((a, b) => {
		const difference = rowStartedAt(a) - rowStartedAt(b);
		if (difference !== 0) return direction === "asc" ? difference : -difference;
		const aKey = a.type === "activity" ? a.activity.id : a.key;
		const bKey = b.type === "activity" ? b.activity.id : b.key;
		return direction === "asc"
			? aKey.localeCompare(bKey)
			: bKey.localeCompare(aKey);
	});

/**
 * Builds Activity History display rows from two independent timelines:
 *
 *  - command/tool rows describe execution state;
 *  - MCP request spans describe whether the assistant is waiting for a tool.
 *
 * Current Idle is always first when there are zero active MCP requests. Running
 * command/tool rows are pinned immediately below it. Historical Idle is derived
 * only from gaps in the union of MCP request spans, never from gaps between
 * filtered command rows.
 */
export const buildActivityDisplayRows = (
	activity: readonly WorkspaceCommandActivity[],
	mcpRequests: readonly WorkspaceMCPRequestActivity[],
	includeIdle: boolean,
	showActivity: boolean,
	sortBy: WorkspaceCommandActivitySort,
	sortDirection: WorkspaceCommandActivitySortDirection,
): ActivityDisplayRow[] => {
	const running = showActivity
		? activity
				.filter((item) => item.status === "running")
				.map((item) => ({ type: "activity" as const, activity: item }))
		: [];
	const completed = showActivity
		? activity
				.filter((item) => item.status !== "running")
				.map((item) => ({ type: "activity" as const, activity: item }))
		: [];

	if (!includeIdle) return [...running, ...completed];

	const currentIdle = currentIdleRow(mcpRequests);
	const historicalIdle = historicalIdleRows(mcpRequests, activity);
	let tail: ActivityDisplayRow[];
	if (sortBy === "started") {
		tail = chronologicalRows([...completed, ...historicalIdle], sortDirection);
	} else if (!showActivity) {
		// Idle-only views are timelines even if an old browser preference still
		// points at a command-only sort column.
		tail = chronologicalRows([...historicalIdle], "desc");
	} else {
		// Tool/source/input/exit sorting has no meaningful value for Idle. Preserve
		// the server's order for real rows rather than pretending otherwise.
		tail = completed;
	}

	return [...(currentIdle ? [currentIdle] : []), ...running, ...tail];
};
