package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "mcp-tool-eval:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("mcp-tool-eval", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	model := flags.String("model", os.Getenv("MCP_TOOL_EVAL_MODEL"), "model name")
	baseURL := flags.String("base-url", envOr("MCP_TOOL_EVAL_BASE_URL", "https://api.openai.com/v1"), "OpenAI Responses compatible base URL")
	apiKeyEnv := flags.String("api-key-env", "MCP_TOOL_EVAL_API_KEY", "environment variable containing the bearer credential, or '-' for no Authorization header")
	repeats := flags.Int("repeat", 5, "number of model selections per scenario and catalog")
	catalogsFlag := flags.String("catalogs", "baseline,candidate,specialized", "comma-separated catalogs: baseline,candidate,specialized")
	filter := flags.String("filter", "", "run only scenarios whose id or category contains this value")
	scenariosPath := flags.String("scenarios", "", "scenario JSON path, defaults to the embedded history-derived suite")
	candidatePath := flags.String("candidate", "", "compact candidate overlay JSON path, defaults to the embedded proposal")
	specializedPath := flags.String("specialized", "", "specialized remote-tool overlay JSON path, defaults to the embedded proposal")
	outputPath := flags.String("output", "", "write full JSON results to this path")
	listOnly := flags.Bool("list", false, "list catalogs and scenarios without calling a model")
	verbose := flags.Bool("verbose", false, "print every model selection")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *repeats < 1 {
		return xerrors.New("repeat must be at least 1")
	}

	baseline, err := currentDeveloperCatalog(ctx)
	if err != nil {
		return err
	}
	candidateOverlay, err := loadCandidateOverlay(*candidatePath)
	if err != nil {
		return err
	}
	candidate, err := applyOverlay(baseline, candidateOverlay)
	if err != nil {
		return err
	}

	specializedOverlay, err := loadSpecializedOverlay(*specializedPath)
	if err != nil {
		return err
	}
	sharedOverlay := catalogOverlay{Version: 1, AddTools: candidateOverlay.AddTools}
	specializedBase, err := applyOverlay(baseline, sharedOverlay)
	if err != nil {
		return xerrors.Errorf("apply shared candidate tools: %w", err)
	}
	specialized, err := applyOverlay(specializedBase, specializedOverlay)
	if err != nil {
		return xerrors.Errorf("apply specialized remote tools: %w", err)
	}

	set, err := loadScenarioSet(*scenariosPath)
	if err != nil {
		return err
	}
	set.Scenarios = filterScenarios(set.Scenarios, *filter)
	if len(set.Scenarios) == 0 {
		return xerrors.Errorf("no scenarios matched filter %q", *filter)
	}
	allCatalogs := map[string][]toolSpec{
		"baseline":    baseline,
		"candidate":   candidate,
		"specialized": specialized,
	}
	if err := validateScenarios(set, allCatalogs); err != nil {
		return err
	}
	selectedCatalogs, err := selectCatalogs(*catalogsFlag, allCatalogs)
	if err != nil {
		return err
	}

	infos := make(map[string]catalogInfo, len(selectedCatalogs))
	for name, tools := range selectedCatalogs {
		info, err := catalogStats(tools)
		if err != nil {
			return xerrors.Errorf("measure %s catalog: %w", name, err)
		}
		infos[name] = info
	}

	if *listOnly {
		return printInventory(os.Stdout, selectedCatalogs, infos, set.Scenarios)
	}
	if strings.TrimSpace(*model) == "" {
		return xerrors.New("model is required; set --model or MCP_TOOL_EVAL_MODEL")
	}
	apiKey := ""
	if *apiKeyEnv != "-" {
		apiKey = os.Getenv(*apiKeyEnv)
		if apiKey == "" {
			return xerrors.Errorf("%s is not set; provide a bearer credential or use --api-key-env=- for an unauthenticated endpoint", *apiKeyEnv)
		}
	}

	selector := newResponsesSelector(*baseURL, apiKey, *model)
	report := runReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Model:       *model,
		Repeats:     *repeats,
		Catalogs:    infos,
	}

	catalogNames := make([]string, 0, len(selectedCatalogs))
	for name := range selectedCatalogs {
		catalogNames = append(catalogNames, name)
	}
	sort.Strings(catalogNames)
	for _, catalogName := range catalogNames {
		tools := selectedCatalogs[catalogName]
		for _, sc := range set.Scenarios {
			expected := expectedForCatalog(sc, catalogName)
			scoredScenario := sc
			scoredScenario.Expected = expected
			for iteration := 1; iteration <= *repeats; iteration++ {
				started := time.Now()
				selected, usage, selectErr := selector.Select(ctx, sc.Prompt, tools)
				result := runResult{
					Catalog:      catalogName,
					ScenarioID:   sc.ID,
					Category:     sc.Category,
					ExpectedTool: expected.Tool,
					Iteration:    iteration,
					Selection:    selected,
					Usage:        usage,
					LatencyMS:    time.Since(started).Milliseconds(),
				}
				if selectErr != nil {
					result.Error = selectErr.Error()
				} else {
					result.ToolMatch, result.ArgumentsMatch, result.SchemaValid, result.Forbidden, result.Passed = scoreSelection(scoredScenario, selected, tools)
				}
				report.Results = append(report.Results, result)
				if *verbose {
					status := "FAIL"
					if result.Passed {
						status = "PASS"
					}
					if result.Error != "" {
						status = "ERROR"
					}
					if err := writef(os.Stdout, "%s %s %d/%d %s tool=%s\n", catalogName, sc.ID, iteration, *repeats, status, selected.Tool); err != nil {
						return xerrors.Errorf("write verbose result: %w", err)
					}
				}
			}
		}
	}

	if err := printSummary(os.Stdout, report); err != nil {
		return err
	}
	if *outputPath != "" {
		if err := writeReport(*outputPath, report); err != nil {
			return err
		}
		if err := writef(os.Stdout, "results: %s\n", *outputPath); err != nil {
			return xerrors.Errorf("write result path: %w", err)
		}
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func filterScenarios(scenarios []scenario, filter string) []scenario {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return scenarios
	}
	out := make([]scenario, 0, len(scenarios))
	for _, sc := range scenarios {
		if strings.Contains(strings.ToLower(sc.ID), filter) || strings.Contains(strings.ToLower(sc.Category), filter) {
			out = append(out, sc)
		}
	}
	return out
}

func selectCatalogs(raw string, available map[string][]toolSpec) (map[string][]toolSpec, error) {
	selected := map[string][]toolSpec{}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		tools, ok := available[name]
		if !ok {
			return nil, xerrors.Errorf("unknown catalog %q", name)
		}
		selected[name] = tools
	}
	if len(selected) == 0 {
		return nil, xerrors.New("at least one catalog is required")
	}
	return selected, nil
}

func printInventory(w io.Writer, catalogs map[string][]toolSpec, infos map[string]catalogInfo, scenarios []scenario) error {
	names := make([]string, 0, len(catalogs))
	for name := range catalogs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info := infos[name]
		if err := writef(w, "catalog %s: %d tools, %d schema bytes\n", name, info.ToolCount, info.SchemaBytes); err != nil {
			return xerrors.Errorf("write catalog summary: %w", err)
		}
		for _, tool := range catalogs[name] {
			if err := writef(w, "  %s\n", tool.Name); err != nil {
				return xerrors.Errorf("write catalog tool: %w", err)
			}
		}
	}
	if err := writef(w, "scenarios: %d\n", len(scenarios)); err != nil {
		return xerrors.Errorf("write scenario count: %w", err)
	}
	for _, sc := range scenarios {
		expected := sc.Expected.Tool
		if specialized, ok := sc.ExpectedByCatalog["specialized"]; ok && specialized.Tool != expected {
			expected += " | specialized:" + specialized.Tool
		}
		if err := writef(w, "  %s [%s] -> %s\n", sc.ID, sc.Category, expected); err != nil {
			return xerrors.Errorf("write scenario: %w", err)
		}
	}
	return nil
}

func printSummary(w io.Writer, report runReport) error {
	type aggregate struct {
		total        int
		passed       int
		toolMatch    int
		argsMatch    int
		schemaValid  int
		forbidden    int
		errors       int
		inputTokens  int64
		outputTokens int64
		latencyMS    int64
	}
	add := func(agg *aggregate, result runResult) {
		agg.total++
		if result.Passed {
			agg.passed++
		}
		if result.ToolMatch {
			agg.toolMatch++
		}
		if result.ArgumentsMatch {
			agg.argsMatch++
		}
		if result.SchemaValid {
			agg.schemaValid++
		}
		if result.Forbidden {
			agg.forbidden++
		}
		if result.Error != "" {
			agg.errors++
		}
		agg.inputTokens += result.Usage.InputTokens
		agg.outputTokens += result.Usage.OutputTokens
		agg.latencyMS += result.LatencyMS
	}

	aggregates := map[string]*aggregate{}
	byCategory := map[string]map[string]*aggregate{}
	confusions := map[string]map[string]int{}
	for _, result := range report.Results {
		agg := aggregates[result.Catalog]
		if agg == nil {
			agg = &aggregate{}
			aggregates[result.Catalog] = agg
		}
		add(agg, result)

		categories := byCategory[result.Catalog]
		if categories == nil {
			categories = map[string]*aggregate{}
			byCategory[result.Catalog] = categories
		}
		categoryAgg := categories[result.Category]
		if categoryAgg == nil {
			categoryAgg = &aggregate{}
			categories[result.Category] = categoryAgg
		}
		add(categoryAgg, result)

		if result.Error == "" && !result.ToolMatch {
			catalogConfusions := confusions[result.Catalog]
			if catalogConfusions == nil {
				catalogConfusions = map[string]int{}
				confusions[result.Catalog] = catalogConfusions
			}
			key := result.ExpectedTool + " -> " + result.Selection.Tool
			catalogConfusions[key]++
		}
	}

	names := make([]string, 0, len(aggregates))
	for name := range aggregates {
		names = append(names, name)
	}
	sort.Strings(names)
	if err := writef(w, "model: %s\n", report.Model); err != nil {
		return xerrors.Errorf("write model summary: %w", err)
	}
	for _, name := range names {
		agg := aggregates[name]
		info := report.Catalogs[name]
		if err := writef(w, "%s: pass=%d/%d (%.1f%%), tool=%d/%d, args=%d/%d, schema=%d/%d, forbidden=%d, errors=%d, tools=%d, schema_bytes=%d, avg_input_tokens=%.0f, avg_latency_ms=%.0f\n",
			name,
			agg.passed, agg.total, percentage(agg.passed, agg.total),
			agg.toolMatch, agg.total,
			agg.argsMatch, agg.total,
			agg.schemaValid, agg.total,
			agg.forbidden,
			agg.errors,
			info.ToolCount,
			info.SchemaBytes,
			average(agg.inputTokens, agg.total),
			average(agg.latencyMS, agg.total),
		); err != nil {
			return xerrors.Errorf("write catalog result: %w", err)
		}

		categoryNames := make([]string, 0, len(byCategory[name]))
		for category := range byCategory[name] {
			categoryNames = append(categoryNames, category)
		}
		sort.Strings(categoryNames)
		for _, category := range categoryNames {
			categoryAgg := byCategory[name][category]
			if err := writef(w, "  %s: pass=%d/%d (%.1f%%), tool=%d/%d, forbidden=%d, errors=%d\n",
				category,
				categoryAgg.passed, categoryAgg.total, percentage(categoryAgg.passed, categoryAgg.total),
				categoryAgg.toolMatch, categoryAgg.total,
				categoryAgg.forbidden,
				categoryAgg.errors,
			); err != nil {
				return xerrors.Errorf("write category result: %w", err)
			}
		}

		pairs := sortedConfusions(confusions[name])
		if len(pairs) > 0 {
			if err := writef(w, "  tool confusions:\n"); err != nil {
				return xerrors.Errorf("write confusion heading: %w", err)
			}
			for _, pair := range pairs {
				if err := writef(w, "    %s: %d\n", pair.name, pair.count); err != nil {
					return xerrors.Errorf("write confusion: %w", err)
				}
			}
		}
	}
	return nil
}

type confusionCount struct {
	name  string
	count int
}

func sortedConfusions(confusions map[string]int) []confusionCount {
	pairs := make([]confusionCount, 0, len(confusions))
	for name, count := range confusions {
		pairs = append(pairs, confusionCount{name: name, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	return pairs
}

func writef(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

func percentage(value, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}

func average(value int64, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) / float64(total)
}

func writeReport(path string, report runReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return xerrors.Errorf("marshal report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return xerrors.Errorf("write report: %w", err)
	}
	return nil
}
