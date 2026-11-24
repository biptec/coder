import type { Preset, PreviewParameter } from "#/api/typesGenerated";

/** Remember explicit opt-ins when conditional parameters disappear from a preview. */
export const mergePresetMutableParameterNames = (
	previous: ReadonlySet<string>,
	parameters: readonly Pick<PreviewParameter, "name" | "styling">[],
): ReadonlySet<string> => {
	const next = new Set(previous);
	for (const parameter of parameters) {
		if (parameter.styling.preset_mutable) {
			next.add(parameter.name);
		} else {
			next.delete(parameter.name);
		}
	}
	return next.size === previous.size &&
		[...next].every((name) => previous.has(name))
		? previous
		: next;
};

/**
 * A no-prebuild preset may be used as an editable prefill only when every
 * parameter it sets explicitly opts in through styling.preset_mutable.
 * Existing presets remain locked by default, and prebuild presets are always
 * locked because their preset ID is part of the prebuild contract.
 */
export const isEditablePrefillPreset = (
	preset: Preset,
	mutableParameterNames: ReadonlySet<string>,
): boolean => {
	if (
		preset.DesiredPrebuildInstances !== null &&
		preset.DesiredPrebuildInstances > 0
	) {
		return false;
	}

	return (
		preset.Parameters.length > 0 &&
		preset.Parameters.every(({ Name }) => mutableParameterNames.has(Name))
	);
};
