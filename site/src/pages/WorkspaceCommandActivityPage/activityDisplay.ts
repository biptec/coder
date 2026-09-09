import type { WorkspaceCommandActivity } from "#/api/typesGenerated";

export type ActivityDisplayRow =
	| { type: "activity"; activity: WorkspaceCommandActivity }
	| {
			type: "idle";
			key: string;
			startedAt: string;
			finishedAt: string;
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

const idleBetween = (
	first: WorkspaceCommandActivity,
	second: WorkspaceCommandActivity,
): ActivityDisplayRow | undefined => {
	const firstStarted = new Date(first.started_at).getTime();
	const secondStarted = new Date(second.started_at).getTime();
	const [earlier, later, earlierStarted, laterStarted] =
		firstStarted <= secondStarted
			? [first, second, firstStarted, secondStarted]
			: [second, first, secondStarted, firstStarted];

	// A still-running request/process means the two visible activities overlap in
	// time, so there is no known idle interval between them.
	if (!earlier.finished_at) return undefined;
	const earlierFinished = new Date(earlier.finished_at).getTime();
	if (!Number.isFinite(earlierFinished) || earlierFinished >= laterStarted) {
		return undefined;
	}

	const startedAt = new Date(
		Math.max(earlierStarted, earlierFinished),
	).toISOString();
	const finishedAt = new Date(laterStarted).toISOString();
	return {
		type: "idle",
		key: `idle:${earlier.id}:${later.id}`,
		startedAt,
		finishedAt,
	};
};

/**
 * Inserts display-only Idle rows between adjacent real rows returned by the
 * already-filtered REST page. Idle is never persisted, paginated, selected, or
 * deleted. Changing the visible activity filters therefore automatically
 * changes the idle intervals without a server-side timeline model.
 */
export const buildActivityDisplayRows = (
	activity: readonly WorkspaceCommandActivity[],
	includeIdle: boolean,
	showActivity: boolean,
): ActivityDisplayRow[] => {
	const rows: ActivityDisplayRow[] = [];
	for (let index = 0; index < activity.length; index++) {
		const current = activity[index];
		if (showActivity) rows.push({ type: "activity", activity: current });
		if (!includeIdle || index + 1 >= activity.length) continue;
		const idle = idleBetween(current, activity[index + 1]);
		if (idle) rows.push(idle);
	}
	return rows;
};
