import { type FC, useCallback, useEffect, useState } from "react";
import { Navigate, useNavigate } from "react-router";
import { Blink } from "#/components/Blink/Blink";
import {
	BlinkProvider,
	useBlinkContext,
} from "#/components/Blink/BlinkProvider";
import { Button } from "#/components/Button/Button";
import { ProductLogo } from "#/components/Icons/ProductLogo";
import { Loader } from "#/components/Loader/Loader";
import { useAuthContext } from "#/contexts/auth/AuthProvider";
import { pageTitle } from "#/utils/page";
import { BlinkProviderSetup } from "./BlinkProviderSetup";

function readLS(key: string): string | null {
	try {
		return localStorage.getItem(key);
	} catch {
		return null;
	}
}

function writeLS(key: string, value: string): void {
	try {
		localStorage.setItem(key, value);
	} catch {
		// Storage may be unavailable.
	}
}

/**
 * Shown once after first-user setup when Blink was enabled.
 * Introduces the floating assistant and nudges the user to try it.
 */
const BlinkIntroContent: FC<{ providerConfigured: boolean }> = ({
	providerConfigured,
}) => {
	const navigate = useNavigate();
	const { toggle, open } = useBlinkContext();

	// Mark the intro as seen however the user opens Blink, including
	// clicking the floating button directly. Safe to write mid-session
	// because the parent captures the flag once on mount.
	useEffect(() => {
		if (open) {
			writeLS("blink_intro_completed", "true");
		}
	}, [open]);

	const handleTryBlink = useCallback(() => {
		// The user has seen the intro; don't show it again. They stay
		// on this page to interact with Blink until they leave.
		writeLS("blink_intro_completed", "true");
		toggle();
	}, [toggle]);

	const handleSkip = useCallback(() => {
		writeLS("blink_intro_completed", "true");
		void navigate("/templates");
	}, [navigate]);

	return (
		<>
			<title>{pageTitle("Meet Blink")}</title>
			<div className="grow basis-0 min-h-screen flex justify-center items-center py-12">
				<div className="flex flex-col items-center w-full max-w-[480px] px-4 text-center gap-8">
					<header className="flex flex-col items-center gap-4">
						<ProductLogo className="h-10" />
						<h1 className="text-3xl font-semibold m-0">Meet Blink</h1>
						<p className="text-sm text-content-secondary m-0 leading-relaxed max-w-sm">
							Blink is your Coder assistant. It lives in the bottom-right corner
							of your dashboard and can help you set up templates, create
							workspaces, manage users, and answer questions about your
							deployment.
						</p>
						{!providerConfigured && (
							<p className="text-xs text-content-secondary m-0 leading-relaxed max-w-sm">
								Note: no AI provider is configured yet, so Blink can't respond
								until one is added in Admin settings &gt; AI.
							</p>
						)}
					</header>

					{/* Visual pointer toward the floating button */}
					<div className="flex flex-col items-center gap-3">
						<p className="text-sm text-content-primary m-0 font-medium">
							Click the button in the bottom-right corner to get started, or use
							the button below.
						</p>
						<div className="flex items-center gap-2 text-content-secondary">
							<svg
								className="w-6 h-6 animate-bounce"
								fill="none"
								stroke="currentColor"
								viewBox="0 0 24 24"
								aria-hidden="true"
							>
								<path
									strokeLinecap="round"
									strokeLinejoin="round"
									strokeWidth={2}
									d="M19 14l-7 7m0 0l-7-7m7 7V3"
								/>
							</svg>
						</div>
					</div>

					<div className="flex gap-3">
						<Button variant="outline" onClick={handleSkip}>
							Skip to dashboard
						</Button>
						<Button onClick={handleTryBlink}>Try Blink</Button>
					</div>
				</div>
			</div>

			{/* Blink floats here so user can interact with it */}
			<Blink />
		</>
	);
};

export const BlinkIntroPage: FC = () => {
	const { isLoading, isSignedIn } = useAuthContext();

	// The flow has two steps: configure an AI provider so Blink can
	// actually respond, then meet Blink itself.
	const [step, setStep] = useState<"provider" | "intro">("provider");
	const [providerConfigured, setProviderConfigured] = useState(false);

	// Capture the completion flag once on mount. Re-reading it on every
	// render would yank the user off the page as soon as the flag is
	// written mid-session (e.g. right after they open the panel).
	const [introAlreadyCompleted] = useState(
		() => readLS("blink_intro_completed") === "true",
	);

	if (!isLoading && !isSignedIn) {
		return <Navigate to="/login" replace />;
	}

	if (introAlreadyCompleted) {
		return <Navigate to="/templates" replace />;
	}

	if (isLoading) {
		return <Loader fullscreen />;
	}

	if (step === "provider") {
		return (
			<>
				<title>{pageTitle("Set up Blink")}</title>
				<div className="grow basis-0 min-h-screen flex justify-center items-center py-12">
					<div className="flex flex-col items-center w-full max-w-[480px] px-4">
						<BlinkProviderSetup
							onComplete={() => {
								setProviderConfigured(true);
								setStep("intro");
							}}
							onSkip={() => setStep("intro")}
						/>
					</div>
				</div>
			</>
		);
	}

	return (
		<BlinkProvider forceEnabled>
			<BlinkIntroContent providerConfigured={providerConfigured} />
		</BlinkProvider>
	);
};
