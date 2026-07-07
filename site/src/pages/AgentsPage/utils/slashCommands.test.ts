import { describe, expect, it } from "vitest";
import type * as TypesGen from "#/api/typesGenerated";
import {
	type ChatSlashCommand,
	COMPACT_SLASH_COMMAND,
	chatSlashCommandTriggerText,
	filterChatSlashCommands,
	slashMenuItemTriggerText,
	slashMenuItemValue,
} from "./slashCommands";

const command = (name: string, description: string): ChatSlashCommand => ({
	name,
	description,
});

const skill: TypesGen.UserSkillMetadata = {
	id: "skill-1",
	name: "compact",
	description: "A personal skill that shares a command name",
	created_at: "2026-05-08T00:00:00Z",
	updated_at: "2026-05-08T00:00:00Z",
};

describe("filterChatSlashCommands", () => {
	const commands = [
		command("deploy", "Ship production changes"),
		command("compact", "Summarize the conversation"),
		command("review", "Review compact diffs"),
	];

	it("sorts unfiltered commands by name", () => {
		expect(
			filterChatSlashCommands(commands, "").map(({ name }) => name),
		).toEqual(["compact", "deploy", "review"]);
	});

	it("ranks name prefix over substring over description", () => {
		expect(
			filterChatSlashCommands(commands, "compact").map(({ name }) => name),
		).toEqual(["compact", "review"]);
	});

	it("matches case-insensitively", () => {
		expect(
			filterChatSlashCommands(commands, "COMP").map(({ name }) => name),
		).toEqual(["compact", "review"]);
	});

	it("returns no matches for unrelated queries", () => {
		expect(filterChatSlashCommands(commands, "zzz")).toEqual([]);
	});
});

describe("slash menu items", () => {
	it("formats trigger text for commands and skills", () => {
		expect(chatSlashCommandTriggerText(COMPACT_SLASH_COMMAND)).toBe("/compact");
		expect(
			slashMenuItemTriggerText({
				kind: "command",
				command: COMPACT_SLASH_COMMAND,
			}),
		).toBe("/compact");
		expect(slashMenuItemTriggerText({ kind: "skill", skill })).toBe("/compact");
	});

	it("disambiguates cmdk values for same-name command and skill", () => {
		const commandValue = slashMenuItemValue({
			kind: "command",
			command: COMPACT_SLASH_COMMAND,
		});
		const skillValue = slashMenuItemValue({ kind: "skill", skill });
		expect(commandValue).not.toBe(skillValue);
	});
});
