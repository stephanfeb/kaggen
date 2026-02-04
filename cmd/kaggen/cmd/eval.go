package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/eval"
	"github.com/yourusername/kaggen/internal/eval/replay"
	"github.com/yourusername/kaggen/internal/model/anthropic"
	"github.com/yourusername/kaggen/internal/model/gemini"
	"github.com/yourusername/kaggen/internal/model/zai"
	"github.com/yourusername/kaggen/internal/tools"
)

var (
	evalSuitePath  string
	evalModelName  string
	evalJudgeModel string
	evalReplayFile string
	evalRecordFile string
	evalCompare    string
	evalOutputJSON string
	evalCategory   string
	evalCaseIDs    []string
	evalVerbose    bool
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Run evaluation test suite",
	Long: `Run evaluation tests to measure agent performance on instruction following and tool calling.

Examples:
  # Run all tests with default model
  kaggen eval -s testdata/eval

  # Run with specific model
  kaggen eval -s testdata/eval --model anthropic/claude-sonnet-4-20250514

  # Record golden baseline
  kaggen eval -s testdata/eval --record baseline.jsonl

  # Replay from recording (deterministic, no API calls)
  kaggen eval -s testdata/eval --replay baseline.jsonl

  # Compare against baseline
  kaggen eval -s testdata/eval --compare baseline.jsonl

  # Run specific category
  kaggen eval -s testdata/eval --category instruction_following

  # Run specific test cases
  kaggen eval -s testdata/eval --case if-001 --case if-002
`,
	RunE: runEval,
}

func init() {
	evalCmd.Flags().StringVarP(&evalSuitePath, "suite", "s", "testdata/eval", "Path to test suite directory")
	evalCmd.Flags().StringVar(&evalModelName, "model", "", "Model to evaluate (e.g., anthropic/claude-sonnet-4)")
	evalCmd.Flags().StringVar(&evalJudgeModel, "judge", "", "Model for LLM-as-judge (defaults to same as --model)")
	evalCmd.Flags().StringVar(&evalReplayFile, "replay", "", "Replay from recorded file (deterministic, no API calls)")
	evalCmd.Flags().StringVar(&evalRecordFile, "record", "", "Record interactions to file for later replay")
	evalCmd.Flags().StringVar(&evalCompare, "compare", "", "Compare results against baseline file")
	evalCmd.Flags().StringVarP(&evalOutputJSON, "output", "o", "", "Output results to JSON file")
	evalCmd.Flags().StringVar(&evalCategory, "category", "", "Filter to specific category")
	evalCmd.Flags().StringSliceVar(&evalCaseIDs, "case", nil, "Run specific test case(s) by ID")
	evalCmd.Flags().BoolVarP(&evalVerbose, "verbose", "v", false, "Verbose output")
}

func runEval(cmd *cobra.Command, args []string) error {
	// Setup logger
	logLevel := slog.LevelInfo
	if evalVerbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	// Load test cases
	logger.Info("Loading test cases", "path", evalSuitePath)
	cases, err := eval.LoadTestCases(evalSuitePath)
	if err != nil {
		return fmt.Errorf("load test cases: %w", err)
	}

	// Filter cases
	if evalCategory != "" {
		cases = eval.FilterByCategory(cases, evalCategory)
	}
	if len(evalCaseIDs) > 0 {
		cases = eval.FilterByIDs(cases, evalCaseIDs)
	}

	if len(cases) == 0 {
		return fmt.Errorf("no test cases found matching filters")
	}

	logger.Info("Found test cases", "count", len(cases))

	// Setup model
	var evalModel model.Model
	var recorder *replay.Recorder

	if evalReplayFile != "" {
		// Replay mode - load from file
		logger.Info("Loading replay file", "path", evalReplayFile)
		replayer, err := replay.LoadFromFile(evalReplayFile)
		if err != nil {
			return fmt.Errorf("load replay file: %w", err)
		}
		evalModel = replayer
	} else {
		// Live mode - create model
		evalModel, err = createModel(evalModelName, logger)
		if err != nil {
			return err
		}

		// Wrap with recorder if needed
		if evalRecordFile != "" {
			recorder = replay.NewRecorder(evalModel)
			evalModel = recorder
		}
	}

	// Setup judge model (for LLM-as-judge assertions)
	var judgeModel model.Model
	if evalJudgeModel != "" {
		judgeModel, err = createModel(evalJudgeModel, logger)
		if err != nil {
			return fmt.Errorf("create judge model: %w", err)
		}
	} else {
		judgeModel = evalModel
	}

	// Create workspace
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	workspace := cfg.WorkspacePath()

	// Create runner
	runner := eval.NewRunner(
		eval.WithModel(evalModel),
		eval.WithJudgeModel(judgeModel),
		eval.WithTools(tools.DefaultTools(workspace)),
		eval.WithSystemInstruction("You are a helpful AI assistant. Use the available tools to complete tasks."),
		eval.WithConfig(eval.RunConfig{
			ModelName: evalModel.Info().Name,
			MaxTurns:  10,
			Timeout:   2 * time.Minute,
			SuitePath: evalSuitePath,
		}),
	)

	// Run evaluation
	ctx := context.Background()
	logger.Info("Running evaluation...")

	summary, err := runner.RunSuite(ctx, cases)
	if err != nil {
		return fmt.Errorf("run suite: %w", err)
	}

	// Save recording if requested
	if recorder != nil && evalRecordFile != "" {
		if err := recorder.SaveToFile(evalRecordFile); err != nil {
			return fmt.Errorf("save recording: %w", err)
		}
		logger.Info("Saved recording", "path", evalRecordFile)
	}

	// Print results
	printSummary(summary)

	// Compare against baseline if requested
	if evalCompare != "" {
		if err := compareBaseline(evalCompare, summary, logger); err != nil {
			return fmt.Errorf("compare baseline: %w", err)
		}
	}

	// Save JSON output if requested
	if evalOutputJSON != "" {
		data, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal summary: %w", err)
		}
		if err := os.WriteFile(evalOutputJSON, data, 0644); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		logger.Info("Saved results", "path", evalOutputJSON)
	}

	// Exit with error code if any tests failed
	if summary.PassedCases < summary.TotalCases {
		return fmt.Errorf("%d of %d tests failed", summary.TotalCases-summary.PassedCases, summary.TotalCases)
	}

	return nil
}

func createModel(modelName string, logger *slog.Logger) (model.Model, error) {
	// Check for API keys (priority: ZAI > Gemini > Anthropic)
	zaiKey := config.ZaiAPIKey()
	geminiKey := config.GeminiAPIKey()
	anthropicKey := config.AnthropicAPIKey()

	// If model name is specified, use that provider
	if modelName != "" {
		switch {
		case strings.HasPrefix(modelName, "zai/"):
			if zaiKey == "" {
				return nil, fmt.Errorf("ZAI_API_KEY not set")
			}
			name := strings.TrimPrefix(modelName, "zai/")
			logger.Info("Using ZAI model", "model", name)
			return zai.NewAdapter(zaiKey, name), nil

		case strings.HasPrefix(modelName, "gemini/"):
			if geminiKey == "" {
				return nil, fmt.Errorf("GEMINI_API_KEY not set")
			}
			name := strings.TrimPrefix(modelName, "gemini/")
			logger.Info("Using Gemini model", "model", name)
			return gemini.NewAdapter(geminiKey, name), nil

		case strings.HasPrefix(modelName, "anthropic/"):
			if anthropicKey == "" {
				return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
			}
			name := strings.TrimPrefix(modelName, "anthropic/")
			logger.Info("Using Anthropic model", "model", name)
			return anthropic.NewAdapter(anthropicKey, name), nil

		default:
			return nil, fmt.Errorf("unknown model format: %s (use prefix: zai/, gemini/, or anthropic/)", modelName)
		}
	}

	// Auto-detect based on available keys
	if zaiKey != "" {
		logger.Info("Using ZAI model (auto-detected)", "model", "glm-4.7")
		return zai.NewAdapter(zaiKey, "glm-4.7"), nil
	}
	if geminiKey != "" {
		logger.Info("Using Gemini model (auto-detected)", "model", "gemini-3-pro-preview")
		return gemini.NewAdapter(geminiKey, "gemini-3-pro-preview"), nil
	}
	if anthropicKey != "" {
		logger.Info("Using Anthropic model (auto-detected)", "model", "claude-sonnet-4-20250514")
		return anthropic.NewAdapter(anthropicKey, "claude-sonnet-4-20250514"), nil
	}

	return nil, fmt.Errorf("no API key found (set ZAI_API_KEY, GEMINI_API_KEY, or ANTHROPIC_API_KEY)")
}

func printSummary(summary *eval.EvalSummary) {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("                     EVALUATION RESULTS")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	// Overall stats
	passSymbol := "✓"
	if summary.PassedCases < summary.TotalCases {
		passSymbol = "✗"
	}
	fmt.Printf("  %s Pass Rate: %.1f%% (%d/%d)\n", passSymbol, summary.PassRate*100, summary.PassedCases, summary.TotalCases)
	fmt.Printf("    Avg Score: %.2f\n", summary.AvgScore)
	fmt.Println()

	// By category
	if len(summary.CategoryScores) > 0 {
		fmt.Println("  By Category:")
		for name, cat := range summary.CategoryScores {
			symbol := "✓"
			if cat.PassRate < 1.0 {
				symbol = "✗"
			}
			fmt.Printf("    %s %s: %.1f%% (%d/%d), avg=%.2f\n",
				symbol, name, cat.PassRate*100, cat.PassedCases, cat.TotalCases, cat.AvgScore)
		}
		fmt.Println()
	}

	// Individual results
	fmt.Println("  Results:")
	for _, r := range summary.Results {
		symbol := "✓"
		if !r.Passed {
			symbol = "✗"
		}
		fmt.Printf("    %s [%s] %s (score=%.2f, turns=%d)\n",
			symbol, r.CaseID, r.CaseName, r.Score, r.TurnCount)

		// Show failed assertions
		if !r.Passed {
			for _, a := range r.Assertions {
				if !a.Passed {
					fmt.Printf("        └─ %s: %s\n", a.Type, a.Reason)
				}
			}
		}
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
}

func compareBaseline(baselinePath string, current *eval.EvalSummary, logger *slog.Logger) error {
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		return fmt.Errorf("read baseline: %w", err)
	}

	var baseline eval.EvalSummary
	if err := json.Unmarshal(data, &baseline); err != nil {
		return fmt.Errorf("parse baseline: %w", err)
	}

	fmt.Println()
	fmt.Println("  Comparison vs Baseline:")
	fmt.Printf("    Pass Rate: %.1f%% → %.1f%% (%+.1f%%)\n",
		baseline.PassRate*100, current.PassRate*100, (current.PassRate-baseline.PassRate)*100)
	fmt.Printf("    Avg Score: %.2f → %.2f (%+.2f)\n",
		baseline.AvgScore, current.AvgScore, current.AvgScore-baseline.AvgScore)

	// Check for regressions
	baselineMap := make(map[string]eval.EvalResult)
	for _, r := range baseline.Results {
		baselineMap[r.CaseID] = r
	}

	var regressions []string
	for _, r := range current.Results {
		if base, ok := baselineMap[r.CaseID]; ok {
			if r.Score < base.Score-0.05 { // 5% regression threshold
				regressions = append(regressions, fmt.Sprintf("%s: %.2f → %.2f", r.CaseID, base.Score, r.Score))
			}
		}
	}

	if len(regressions) > 0 {
		fmt.Println()
		fmt.Println("  ⚠ REGRESSIONS DETECTED:")
		for _, reg := range regressions {
			fmt.Printf("    - %s\n", reg)
		}
	}

	return nil
}
