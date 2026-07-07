import type * as TypesGen from "#/api/typesGenerated";
import { getWorkspaceAgents } from "#/utils/workspace";
import type { AgentContextUsage } from "../AgentChatInput";
import type { ModelSelectorOption } from "../ChatElements";
import { asString } from "../ChatElements/runtimeTypeUtils";
import { asNonEmptyString } from "./blockUtils";

export const extractContextUsageFromMessage = (
	message: TypesGen.ChatMessage,
): AgentContextUsage | null => {
	const usage = message.usage;
	if (!usage) {
		return null;
	}

	const inputTokens = usage.input_tokens;
	const outputTokens = usage.output_tokens;
	const reasoningTokens = usage.reasoning_tokens;
	const cacheCreationTokens = usage.cache_creation_tokens;
	const cacheReadTokens = usage.cache_read_tokens;
	const contextLimitTokens = usage.context_limit;

	const components = [
		inputTokens,
		outputTokens,
		cacheReadTokens,
		cacheCreationTokens,
		reasoningTokens,
	].filter((value): value is number => value !== undefined);
	const usedTokens =
		components.length > 0
			? components.reduce((total, value) => total + value, 0)
			: undefined;

	return {
		usedTokens,
		contextLimitTokens,
		inputTokens,
		outputTokens,
		cacheReadTokens,
		cacheCreationTokens,
		reasoningTokens,
	};
};

export const getLatestContextUsage = (
	messages: readonly TypesGen.ChatMessage[],
): AgentContextUsage | null => {
	for (let index = messages.length - 1; index >= 0; index -= 1) {
		const usage = extractContextUsageFromMessage(messages[index]);
		if (usage) {
			return usage;
		}
	}
	return null;
};

type ChatWithHierarchyMetadata = TypesGen.Chat & {
	readonly parent_chat_id?: string;
};

export const getParentChatID = (
	chat: TypesGen.Chat | undefined,
): string | undefined => {
	return asNonEmptyString(
		(chat as ChatWithHierarchyMetadata | undefined)?.parent_chat_id,
	);
};

export const resolveModelFromChatConfig = (
	modelConfig: unknown,
	modelOptions: readonly ModelSelectorOption[],
): string => {
	if (modelOptions.length === 0) {
		return "";
	}

	if (!modelConfig || typeof modelConfig !== "object") {
		return modelOptions[0]?.id ?? "";
	}

	const typedModelConfig = modelConfig as Record<string, unknown>;
	const model = asString(typedModelConfig.model);

	if (model) {
		const match = modelOptions.find((option) => option.id === model);
		if (match) {
			return match.id;
		}
	}

	return modelOptions[0]?.id ?? "";
};

// Chat-designated agents use this naming convention; mirrors
// agentselect.Suffix on the backend.
const chatAgentSuffix = "-coderd-chat";

// compareChatAgents replicates the backend comparator in
// agentselect.FindChatAgent: display_order ASC, then case-insensitive
// name, then name, then id. Plain string comparisons (not
// localeCompare) match Go's byte-order string compare on these
// ASCII-only fields.
const compareChatAgents = (
	a: TypesGen.WorkspaceAgent,
	b: TypesGen.WorkspaceAgent,
): number => {
	if (a.display_order !== b.display_order) {
		return a.display_order - b.display_order;
	}
	const aLower = a.name.toLowerCase();
	const bLower = b.name.toLowerCase();
	if (aLower !== bLower) {
		return aLower < bLower ? -1 : 1;
	}
	if (a.name !== b.name) {
		return a.name < b.name ? -1 : 1;
	}
	if (a.id !== b.id) {
		return a.id < b.id ? -1 : 1;
	}
	return 0;
};

// selectChatAgent mirrors the backend's agentselect.FindChatAgent so
// affordances gate on the agent the server will actually use before a
// chat binds one: root agents only, re-sorted globally with the
// backend's comparator (the API response is also sorted, but only
// within each resource, so flattened order can diverge), preferring a
// `-coderd-chat` suffixed agent. When several agents carry the suffix
// the backend refuses to auto-select; the first match keeps the
// affordance visible so the server's actionable error surfaces on use.
const selectChatAgent = (
	agents: readonly TypesGen.WorkspaceAgent[],
): TypesGen.WorkspaceAgent | undefined => {
	const rootAgents = agents
		.filter((agent) => !agent.parent_id)
		.sort(compareChatAgents);
	return (
		rootAgents.find((agent) =>
			agent.name.toLowerCase().endsWith(chatAgentSuffix),
		) ?? rootAgents[0]
	);
};

export const getWorkspaceAgent = (
	workspace: TypesGen.Workspace | undefined,
	workspaceAgentId: string | undefined,
): TypesGen.WorkspaceAgent | undefined => {
	if (!workspace) {
		return undefined;
	}
	const agents = getWorkspaceAgents(workspace);
	if (agents.length === 0) {
		return undefined;
	}
	return (
		agents.find((agent) => agent.id === workspaceAgentId) ??
		selectChatAgent(agents)
	);
};
