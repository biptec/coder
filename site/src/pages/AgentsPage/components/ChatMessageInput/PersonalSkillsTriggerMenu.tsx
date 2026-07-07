import { useLayoutEffect, useState } from "react";
import {
	Command,
	CommandEmpty,
	CommandGroup,
	CommandItem,
	CommandList,
} from "#/components/Command/Command";
import {
	Popover,
	PopoverAnchor,
	PopoverContent,
} from "#/components/Popover/Popover";
import {
	type SlashMenuItem,
	slashMenuItemTriggerText,
	slashMenuItemValue,
} from "../../utils/slashCommands";

// Prevent zero-height anchors when the browser returns a degenerate caret rect.
const MIN_ANCHOR_HEIGHT_PX = 16;

export type CaretAnchorRect = {
	top: number;
	left: number;
	height: number;
};

type PersonalSkillsTriggerMenuProps = {
	open: boolean;
	anchorRect: CaretAnchorRect | null;
	query: string;
	items: readonly SlashMenuItem[];
	isLoading?: boolean;
	isError?: boolean;
	selectedIndex: number;
	onSelectedIndexChange: (index: number) => void;
	onSelect: (item: SlashMenuItem) => void;
	onClose: () => void;
};

type PersonalSkillsMenuState = {
	anchorRect: CaretAnchorRect;
	query: string;
	items: readonly SlashMenuItem[];
	isLoading?: boolean;
	isError?: boolean;
	selectedIndex: number;
};

export const PersonalSkillsTriggerMenu = ({
	open,
	anchorRect,
	query,
	items,
	isLoading,
	isError,
	selectedIndex,
	onSelectedIndexChange,
	onSelect,
	onClose,
}: PersonalSkillsTriggerMenuProps) => {
	const [lastOpenMenuState, setLastOpenMenuState] =
		useState<PersonalSkillsMenuState | null>(null);
	const isAnchoredOpen = open && anchorRect !== null;
	const activeMenuState: PersonalSkillsMenuState | null = isAnchoredOpen
		? {
				anchorRect,
				query,
				items,
				isLoading,
				isError,
				selectedIndex,
			}
		: null;
	const menuState = activeMenuState ?? lastOpenMenuState;
	const menuAnchorRect = menuState?.anchorRect ?? null;
	const menuItems = menuState?.items ?? [];
	const menuSelectedIndex = menuState?.selectedIndex ?? -1;

	useLayoutEffect(() => {
		if (!isAnchoredOpen) {
			return;
		}
		setLastOpenMenuState({
			anchorRect,
			query,
			items,
			isLoading,
			isError,
			selectedIndex,
		});
	}, [
		anchorRect,
		isAnchoredOpen,
		isError,
		isLoading,
		query,
		selectedIndex,
		items,
	]);

	const handleHighlightedValueChange = (value: string) => {
		const nextIndex = menuItems.findIndex(
			(item) => slashMenuItemValue(item) === value,
		);
		if (nextIndex >= 0) {
			onSelectedIndexChange(nextIndex);
		}
	};

	// Groups render per item kind straight from the combined list.
	// Commands always precede skills in it, so the partitioned groups
	// match keyboard selection order. Boolean flags (not derived
	// arrays) keep the component memoizable by the React compiler.
	const hasCommandItems = menuItems.some((item) => item.kind === "command");
	const hasSkillItems = menuItems.some((item) => item.kind === "skill");
	const highlightedItem = menuItems[menuSelectedIndex];
	const highlightedValue = highlightedItem
		? slashMenuItemValue(highlightedItem)
		: "";

	return (
		<Popover
			open={isAnchoredOpen}
			onOpenChange={(nextOpen) => {
				if (!nextOpen) {
					onClose();
				}
			}}
		>
			{menuAnchorRect && (
				<PopoverAnchor asChild>
					<span
						aria-hidden="true"
						style={{
							position: "fixed",
							top: menuAnchorRect.top,
							left: menuAnchorRect.left,
							width: 1,
							height: Math.max(menuAnchorRect.height, MIN_ANCHOR_HEIGHT_PX),
							pointerEvents: "none",
						}}
					/>
				</PopoverAnchor>
			)}
			<PopoverContent
				align="start"
				side="bottom"
				className="w-80 overflow-hidden p-1 mobile-full-width-dropdown mobile-full-width-dropdown-above-composer"
				onMouseDown={(event) => event.preventDefault()}
				onOpenAutoFocus={(event) => event.preventDefault()}
				onCloseAutoFocus={(event) => event.preventDefault()}
			>
				<Command
					shouldFilter={false}
					loop={false}
					onValueChange={handleHighlightedValueChange}
					value={highlightedValue}
				>
					<CommandList className="max-h-72 border-t-0 mobile-full-width-dropdown-scroll-area">
						{hasCommandItems && (
							<CommandGroup heading="Commands">
								{menuItems.map((item) =>
									item.kind === "command" ? (
										<SlashMenuCommandItem
											key={slashMenuItemValue(item)}
											item={item}
											description={item.command.description}
											onSelect={onSelect}
										/>
									) : null,
								)}
							</CommandGroup>
						)}
						{menuState?.isLoading ? (
							<CommandItem value="loading" disabled>
								Loading personal skills...
							</CommandItem>
						) : menuState?.isError ? (
							<CommandItem value="error" disabled>
								Could not load personal skills. Close and type / again to retry.
							</CommandItem>
						) : hasSkillItems ? (
							<CommandGroup heading="Personal skills">
								{menuItems.map((item) =>
									item.kind === "skill" ? (
										<SlashMenuCommandItem
											key={item.skill.id}
											item={item}
											description={item.skill.description}
											onSelect={onSelect}
										/>
									) : null,
								)}
							</CommandGroup>
						) : !hasCommandItems ? (
							<CommandEmpty>
								{menuState?.query
									? "No personal skills match that query."
									: "No personal skills found."}
							</CommandEmpty>
						) : null}
					</CommandList>
				</Command>
			</PopoverContent>
		</Popover>
	);
};

type SlashMenuCommandItemProps = {
	item: SlashMenuItem;
	description: string;
	onSelect: (item: SlashMenuItem) => void;
};

const SlashMenuCommandItem = ({
	item,
	description,
	onSelect,
}: SlashMenuCommandItemProps) => (
	<CommandItem
		value={slashMenuItemValue(item)}
		className="items-start"
		onSelect={() => onSelect(item)}
	>
		<div className="min-w-0 space-y-1">
			<div className="truncate font-mono text-content-primary text-xs">
				{slashMenuItemTriggerText(item)}
			</div>
			{description.trim() && (
				<div className="line-clamp-2 text-content-secondary text-xs leading-snug">
					{description}
				</div>
			)}
		</div>
	</CommandItem>
);
