import { type RefObject, useEffect, useRef, useState } from "react";

/**
 * Returns true once the element referenced by `ref` has been visible in the
 * viewport at least once and then scrolled above the top of the viewport.
 *
 * The state is sticky: once true, it stays true until the ref changes. Callers
 * typically gate a visual "you missed a required field" cue on the return
 * value combined with an emptiness check, so the cue disappears as soon as
 * the user fills the field in.
 */
export const useHasBeenScrolledPast = (
	ref: RefObject<HTMLElement | null>,
): boolean => {
	const [scrolledPast, setScrolledPast] = useState(false);
	const hasBeenSeen = useRef(false);

	useEffect(() => {
		const el = ref.current;
		if (!el || typeof IntersectionObserver === "undefined") {
			return;
		}

		const observer = new IntersectionObserver(
			(entries) => {
				for (const entry of entries) {
					if (entry.isIntersecting) {
						hasBeenSeen.current = true;
					} else if (hasBeenSeen.current && entry.boundingClientRect.top < 0) {
						setScrolledPast(true);
					}
				}
			},
			{ threshold: 0 },
		);

		observer.observe(el);
		return () => observer.disconnect();
	}, [ref]);

	return scrolledPast;
};
