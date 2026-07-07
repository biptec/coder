import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent } from "storybook/test";
import { filterPersonalSkills } from "../../utils/personalSkills";
import {
	COMPACT_SLASH_COMMAND,
	type SlashMenuItem,
} from "../../utils/slashCommands";
import { PersonalSkillsTriggerMenu } from "./PersonalSkillsTriggerMenu";
import {
	expectNoVisibleText,
	findVisibleText,
	MockSkills,
} from "./storyHelpers";

const skillItems = (skills: typeof MockSkills): SlashMenuItem[] =>
	skills.map((skill) => ({ kind: "skill", skill }));

const MockSkillItems = skillItems(MockSkills);

const compactCommandItem: SlashMenuItem = {
	kind: "command",
	command: COMPACT_SLASH_COMMAND,
};

const meta: Meta<typeof PersonalSkillsTriggerMenu> = {
	title: "components/ChatMessageInput/PersonalSkillsTriggerMenu",
	component: PersonalSkillsTriggerMenu,
	args: {
		open: true,
		anchorRect: { top: 120, left: 80, height: 20 },
		query: "",
		items: MockSkillItems,
		onSelectedIndexChange: fn(),
		selectedIndex: 0,
		onSelect: fn(),
		onClose: fn(),
	},
	decorators: [
		(Story) => (
			<div className="h-80 p-6">
				<p className="text-content-secondary text-sm">
					The menu is anchored to a mock caret position.
				</p>
				<Story />
			</div>
		),
	],
};

export default meta;
type Story = StoryObj<typeof PersonalSkillsTriggerMenu>;

export const Open: Story = {
	play: async () => {
		expect(await findVisibleText("/reviewer")).toBeDefined();
		expect(
			await findVisibleText("Review changed files and suggest fixes."),
		).toBeDefined();
	},
};

export const Loading: Story = {
	args: {
		isLoading: true,
		items: [],
	},
	play: async () => {
		expect(await findVisibleText("Loading personal skills...")).toBeDefined();
	},
};

export const ErrorState: Story = {
	args: {
		isError: true,
		items: [],
	},
	play: async () => {
		expect(
			await findVisibleText(
				"Could not load personal skills. Close and type / again to retry.",
			),
		).toBeDefined();
	},
};

export const Empty: Story = {
	args: {
		items: [],
	},
	play: async () => {
		expect(await findVisibleText("No personal skills found.")).toBeDefined();
	},
};

export const FilteredEmpty: Story = {
	args: {
		query: "xyz",
		items: [],
	},
	play: async () => {
		expect(
			await findVisibleText("No personal skills match that query."),
		).toBeDefined();
	},
};

export const Filtered: Story = {
	args: {
		query: "rev",
		items: skillItems(filterPersonalSkills(MockSkills, "rev")),
	},
	play: async () => {
		expect(await findVisibleText("/reviewer")).toBeDefined();
		await expectNoVisibleText("/docs");
	},
};

export const SelectsByClick: Story = {
	args: {
		onSelect: fn(),
	},
	play: async ({ args }) => {
		await userEvent.click(await findVisibleText("/reviewer"));
		expect(args.onSelect).toHaveBeenCalledTimes(1);
		expect(args.onSelect).toHaveBeenCalledWith(MockSkillItems[0]);
	},
};

// Built-in commands render in a separate "Commands" group above
// personal skills and stay selectable alongside them.
export const WithCommands: Story = {
	args: {
		items: [compactCommandItem, ...MockSkillItems],
	},
	play: async () => {
		expect(await findVisibleText("Commands")).toBeDefined();
		expect(await findVisibleText("/compact")).toBeDefined();
		expect(await findVisibleText("Personal skills")).toBeDefined();
		expect(await findVisibleText("/reviewer")).toBeDefined();
	},
};

// With no personal skills configured, the menu still opens to offer
// the built-in commands without any skills group or empty message.
export const CommandsOnly: Story = {
	args: {
		items: [compactCommandItem],
	},
	play: async () => {
		expect(await findVisibleText("/compact")).toBeDefined();
		expect(
			await findVisibleText(
				"Summarize the conversation so far to free up context window space",
			),
		).toBeDefined();
		await expectNoVisibleText("No personal skills found.");
	},
};

export const SelectsCommandByClick: Story = {
	args: {
		items: [compactCommandItem, ...MockSkillItems],
		onSelect: fn(),
	},
	play: async ({ args }) => {
		await userEvent.click(await findVisibleText("/compact"));
		expect(args.onSelect).toHaveBeenCalledTimes(1);
		expect(args.onSelect).toHaveBeenCalledWith(compactCommandItem);
	},
};
