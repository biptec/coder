import { SparklesIcon } from "lucide-react";
import type { FC } from "react";
import { cn } from "#/utils/cn";

interface BlinkButtonProps {
	open: boolean;
	onToggle: () => void;
	isThinking?: boolean;
	hasUnread?: boolean;
}

export const BlinkButton: FC<BlinkButtonProps> = ({
	open,
	onToggle,
	isThinking = false,
	hasUnread = false,
}) => {
	return (
		<button
			type="button"
			onClick={onToggle}
			aria-label={open ? "Close Blink assistant" : "Open Blink assistant"}
			aria-expanded={open}
			className={cn(
				"fixed bottom-6 right-6 z-50",
				"flex items-center justify-center",
				"w-12 h-12 rounded-full",
				"bg-surface-invert-primary text-surface-primary",
				"shadow-lg hover:scale-105 transition-transform",
				"focus:outline-none focus-visible:ring-2 focus-visible:ring-border-focus",
				hasUnread && !open && "animate-[blink-pulse_2s_ease-in-out_infinite]",
			)}
		>
			<SparklesIcon className="size-5" />

			{isThinking && (
				<span
					className={cn(
						"absolute -top-0.5 -right-0.5",
						"w-3 h-3 rounded-full",
						"bg-content-link",
						"animate-ping",
					)}
				/>
			)}
			{isThinking && (
				<span
					className={cn(
						"absolute -top-0.5 -right-0.5",
						"w-3 h-3 rounded-full",
						"bg-content-link",
					)}
				/>
			)}

			<style>{`
				@keyframes blink-pulse {
					0%, 100% { box-shadow: 0 0 0 0 currentColor; }
					50% { box-shadow: 0 0 0 6px transparent; }
				}
			`}</style>
		</button>
	);
};
