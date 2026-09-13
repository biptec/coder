package main

import (
	_ "embed"
	"encoding/json"
	"os"

	"golang.org/x/xerrors"
)

//go:embed data/scenarios.json
var embeddedScenarios []byte

//go:embed data/candidate.json
var embeddedCandidate []byte

//go:embed data/specialized.json
var embeddedSpecialized []byte

func loadScenarioSet(path string) (scenarioSet, error) {
	data, err := loadData(path, embeddedScenarios)
	if err != nil {
		return scenarioSet{}, err
	}
	var set scenarioSet
	if err := json.Unmarshal(data, &set); err != nil {
		return scenarioSet{}, xerrors.Errorf("decode scenarios: %w", err)
	}
	return set, nil
}

func loadCandidateOverlay(path string) (catalogOverlay, error) {
	return loadOverlay(path, embeddedCandidate, "candidate")
}

func loadSpecializedOverlay(path string) (catalogOverlay, error) {
	return loadOverlay(path, embeddedSpecialized, "specialized")
}

func loadOverlay(path string, fallback []byte, name string) (catalogOverlay, error) {
	data, err := loadData(path, fallback)
	if err != nil {
		return catalogOverlay{}, err
	}
	var overlay catalogOverlay
	if err := json.Unmarshal(data, &overlay); err != nil {
		return catalogOverlay{}, xerrors.Errorf("decode %s catalog: %w", name, err)
	}
	if overlay.Version != 1 {
		return catalogOverlay{}, xerrors.Errorf("unsupported %s catalog version %d", name, overlay.Version)
	}
	return overlay, nil
}

func loadData(path string, fallback []byte) ([]byte, error) {
	if path == "" {
		return fallback, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, xerrors.Errorf("read %q: %w", path, err)
	}
	return data, nil
}
