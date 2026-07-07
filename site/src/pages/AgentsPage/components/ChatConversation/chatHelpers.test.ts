import { describe, expect, it } from "vitest";
import type * as TypesGen from "#/api/typesGenerated";
import { MockChatMessage } from "#/testHelpers/chatEntities";
import type { ModelSelectorOption } from "../ChatElements";
import {
	extractContextUsageFromMessage,
	getLatestContextUsage,
	getParentChatID,
	getWorkspaceAgent,
	resolveModelFromChatConfig,
} from "./chatHelpers";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const buildOption = (
	id: string,
	provider: string,
	model: string,
): ModelSelectorOption => ({
	id,
	provider,
	model,
	displayName: `${provider}/${model}`,
});

// ---------------------------------------------------------------------------
// extractContextUsageFromMessage
// ---------------------------------------------------------------------------

describe("extractContextUsageFromMessage", () => {
	it("returns null when the message has no usage fields", () => {
		expect(extractContextUsageFromMessage(MockChatMessage)).toBeNull();
	});

	it("returns usage when input_tokens is present", () => {
		const msg = { ...MockChatMessage, usage: { input_tokens: 100 } };
		const result = extractContextUsageFromMessage(msg);
		expect(result).not.toBeNull();
		expect(result!.inputTokens).toBe(100);
		expect(result!.usedTokens).toBe(100);
	});

	it("returns usage when output_tokens is present", () => {
		const msg = { ...MockChatMessage, usage: { output_tokens: 50 } };
		const result = extractContextUsageFromMessage(msg);
		expect(result).not.toBeNull();
		expect(result!.outputTokens).toBe(50);
		expect(result!.usedTokens).toBe(50);
	});

	it("sums all token components into usedTokens", () => {
		const msg = {
			...MockChatMessage,
			usage: {
				input_tokens: 10,
				output_tokens: 20,
				reasoning_tokens: 5,
				cache_creation_tokens: 3,
				cache_read_tokens: 2,
			},
		};
		const result = extractContextUsageFromMessage(msg);
		expect(result).not.toBeNull();
		expect(result!.usedTokens).toBe(10 + 20 + 5 + 3 + 2);
		expect(result!.inputTokens).toBe(10);
		expect(result!.outputTokens).toBe(20);
		expect(result!.reasoningTokens).toBe(5);
		expect(result!.cacheCreationTokens).toBe(3);
		expect(result!.cacheReadTokens).toBe(2);
	});

	it("includes contextLimitTokens when context_limit is set", () => {
		const msg = { ...MockChatMessage, usage: { context_limit: 128000 } };
		const result = extractContextUsageFromMessage(msg);
		expect(result).not.toBeNull();
		expect(result!.contextLimitTokens).toBe(128000);
	});

	it("returns usage with only contextLimitTokens and no usedTokens", () => {
		const msg = { ...MockChatMessage, usage: { context_limit: 4096 } };
		const result = extractContextUsageFromMessage(msg);
		expect(result).not.toBeNull();
		expect(result!.usedTokens).toBeUndefined();
		expect(result!.contextLimitTokens).toBe(4096);
	});
});

// ---------------------------------------------------------------------------
// getLatestContextUsage
// ---------------------------------------------------------------------------

describe("getLatestContextUsage", () => {
	it("returns null for an empty message list", () => {
		expect(getLatestContextUsage([])).toBeNull();
	});

	it("returns null when no messages have usage data", () => {
		const messages = [MockChatMessage, { ...MockChatMessage, id: 2 }];
		expect(getLatestContextUsage(messages)).toBeNull();
	});

	it("returns usage from the last message with usage data", () => {
		const messages = [
			{ ...MockChatMessage, id: 1, usage: { input_tokens: 100 } },
			{ ...MockChatMessage, id: 2 },
			{ ...MockChatMessage, id: 3, usage: { input_tokens: 300 } },
		];
		const result = getLatestContextUsage(messages);
		expect(result).not.toBeNull();
		expect(result!.inputTokens).toBe(300);
	});

	it("skips trailing messages without usage and finds the latest one", () => {
		const messages = [
			{ ...MockChatMessage, id: 1, usage: { input_tokens: 50 } },
			{ ...MockChatMessage, id: 2, usage: { input_tokens: 200 } },
			{ ...MockChatMessage, id: 3 },
		];
		const result = getLatestContextUsage(messages);
		expect(result).not.toBeNull();
		expect(result!.inputTokens).toBe(200);
	});
});

// ---------------------------------------------------------------------------
// getParentChatID
// ---------------------------------------------------------------------------

describe("getParentChatID", () => {
	it("returns undefined for undefined chat", () => {
		expect(getParentChatID(undefined)).toBeUndefined();
	});

	it("returns undefined when parent_chat_id is not present", () => {
		const chat = { id: "c1", title: "test" } as TypesGen.Chat;
		expect(getParentChatID(chat)).toBeUndefined();
	});

	it("returns the parent_chat_id when it is a non-empty string", () => {
		const chat = {
			id: "c1",
			title: "test",
			parent_chat_id: "parent-1",
		} as TypesGen.Chat;
		expect(getParentChatID(chat)).toBe("parent-1");
	});

	it("returns undefined when parent_chat_id is an empty string", () => {
		const chat = {
			id: "c1",
			title: "test",
			parent_chat_id: "",
		} as TypesGen.Chat;
		expect(getParentChatID(chat)).toBeUndefined();
	});

	it("returns undefined when parent_chat_id is only whitespace", () => {
		const chat = {
			id: "c1",
			title: "test",
			parent_chat_id: "   ",
		} as TypesGen.Chat;
		expect(getParentChatID(chat)).toBeUndefined();
	});
});

// ---------------------------------------------------------------------------
// resolveModelFromChatConfig
// ---------------------------------------------------------------------------

describe("resolveModelFromChatConfig", () => {
	const options: ModelSelectorOption[] = [
		buildOption("openai:gpt-4", "openai", "gpt-4"),
		buildOption("anthropic:claude-3", "anthropic", "claude-3"),
	];

	it("returns empty string when no model options exist", () => {
		expect(resolveModelFromChatConfig({ model: "gpt-4" }, [])).toBe("");
	});

	it("returns first option when modelConfig is undefined", () => {
		expect(resolveModelFromChatConfig(undefined, options)).toBe("openai:gpt-4");
	});

	it("matches by exact model id", () => {
		const config = { model: "anthropic:claude-3" };
		expect(resolveModelFromChatConfig(config, options)).toBe(
			"anthropic:claude-3",
		);
	});

	it("returns first option when no match is found", () => {
		const config = { model: "unknown-model" };
		expect(resolveModelFromChatConfig(config, options)).toBe("openai:gpt-4");
	});

	it("returns first option when modelConfig is an empty object", () => {
		expect(resolveModelFromChatConfig({}, options)).toBe("openai:gpt-4");
	});
});

// ---------------------------------------------------------------------------
// getWorkspaceAgent
// ---------------------------------------------------------------------------

describe("getWorkspaceAgent", () => {
	const buildAgent = (id: string): TypesGen.WorkspaceAgent =>
		({
			id,
			name: `agent-${id}`,
			display_order: 0,
		}) as TypesGen.WorkspaceAgent;

	const buildWorkspace = (
		agents: TypesGen.WorkspaceAgent[],
	): TypesGen.Workspace =>
		({
			latest_build: {
				resources: [{ agents }],
			},
		}) as unknown as TypesGen.Workspace;

	it("returns undefined when workspace is undefined", () => {
		expect(getWorkspaceAgent(undefined, "agent-1")).toBeUndefined();
	});

	it("returns undefined when there are no agents", () => {
		const ws = buildWorkspace([]);
		expect(getWorkspaceAgent(ws, "agent-1")).toBeUndefined();
	});

	it("returns the matching agent by id", () => {
		const ws = buildWorkspace([buildAgent("a1"), buildAgent("a2")]);
		expect(getWorkspaceAgent(ws, "a2")).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});

	it("returns the first agent when workspaceAgentId does not match", () => {
		const ws = buildWorkspace([buildAgent("a1"), buildAgent("a2")]);
		expect(getWorkspaceAgent(ws, "no-match")).toEqual(
			expect.objectContaining({ id: "a1" }),
		);
	});

	it("returns the first agent when workspaceAgentId is undefined", () => {
		const ws = buildWorkspace([buildAgent("a1")]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a1" }),
		);
	});

	it("collects agents from multiple resources", () => {
		const ws = {
			latest_build: {
				resources: [
					{ agents: [buildAgent("r1-a1")] },
					{ agents: [buildAgent("r2-a1")] },
				],
			},
		} as unknown as TypesGen.Workspace;
		expect(getWorkspaceAgent(ws, "r2-a1")).toEqual(
			expect.objectContaining({ id: "r2-a1" }),
		);
	});

	it("handles resources with no agents array", () => {
		const ws = {
			latest_build: {
				resources: [{ agents: undefined }, { agents: [buildAgent("a1")] }],
			},
		} as unknown as TypesGen.Workspace;
		expect(getWorkspaceAgent(ws, "a1")).toEqual(
			expect.objectContaining({ id: "a1" }),
		);
	});

	// The unbound fallback mirrors the backend's
	// agentselect.FindChatAgent so upload gating and SSH affordances
	// target the agent the server will actually pick.
	const buildNamedAgent = (
		id: string,
		name: string,
		parentId: string | null = null,
		displayOrder = 0,
	): TypesGen.WorkspaceAgent =>
		({
			id,
			name,
			parent_id: parentId,
			display_order: displayOrder,
		}) as TypesGen.WorkspaceAgent;

	it("prefers the -coderd-chat suffixed root agent when unbound", () => {
		const ws = buildWorkspace([
			buildNamedAgent("a1", "alpha"),
			buildNamedAgent("a2", "zeta-coderd-chat"),
		]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});

	it("matches the chat suffix case-insensitively", () => {
		const ws = buildWorkspace([
			buildNamedAgent("a1", "alpha"),
			buildNamedAgent("a2", "main-Coderd-Chat"),
		]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});

	it("re-sorts root agents with the backend comparator when unbound", () => {
		// The API sorts agents per resource, so agents on different
		// resources can arrive out of global order. FindChatAgent
		// sorts all root agents globally by name.
		const ws = {
			latest_build: {
				resources: [
					{ agents: [buildNamedAgent("a1", "zeta")] },
					{ agents: [buildNamedAgent("a2", "alpha")] },
				],
			},
		} as unknown as TypesGen.Workspace;
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});

	it("prioritizes display_order over name like the backend", () => {
		const ws = buildWorkspace([
			buildNamedAgent("a1", "alpha", null, 2),
			buildNamedAgent("a2", "zeta", null, 1),
		]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});

	it("compares names case-insensitively before case-sensitively", () => {
		// Case-sensitive byte order would put "Zeta" (uppercase Z)
		// before "alpha"; the backend compares lowercased names first.
		const ws = buildWorkspace([
			buildNamedAgent("a1", "Zeta"),
			buildNamedAgent("a2", "alpha"),
		]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});

	it("never falls back to child agents", () => {
		const ws = buildWorkspace([
			buildNamedAgent("child", "aaa-sub", "a1"),
			buildNamedAgent("a1", "zeta"),
		]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a1" }),
		);
	});

	it("still resolves child agents by explicit id", () => {
		const ws = buildWorkspace([
			buildNamedAgent("child", "aaa-sub", "a1"),
			buildNamedAgent("a1", "zeta"),
		]);
		expect(getWorkspaceAgent(ws, "child")).toEqual(
			expect.objectContaining({ id: "child" }),
		);
	});

	it("picks the first sorted match when several agents use the suffix", () => {
		const ws = buildWorkspace([
			buildNamedAgent("a1", "zeta-coderd-chat"),
			buildNamedAgent("a2", "alpha-coderd-chat"),
		]);
		expect(getWorkspaceAgent(ws, undefined)).toEqual(
			expect.objectContaining({ id: "a2" }),
		);
	});
});
