package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/proactive"
)

var (
	historyName  string
	historyLimit int
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show proactive job execution history",
	Long:  `Display recent proactive job runs (cron, webhooks, heartbeats).`,
	RunE:  runHistory,
}

func init() {
	historyCmd.Flags().StringVar(&historyName, "name", "", "filter by job name")
	historyCmd.Flags().IntVar(&historyLimit, "limit", 20, "maximum number of results")
	rootCmd.AddCommand(historyCmd)
}

func runHistory(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dbPath := cfg.ProactiveDBPath()
	store, err := proactive.NewHistoryStore(dbPath)
	if err != nil {
		return fmt.Errorf("open history db: %w", err)
	}
	defer store.Close()

	runs, err := store.Query(historyName, historyLimit)
	if err != nil {
		return fmt.Errorf("query history: %w", err)
	}

	if len(runs) == 0 {
		fmt.Println("No job runs found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tSTARTED\tDURATION\tSTATUS\tATTEMPT\tERROR")
	for _, r := range runs {
		errStr := r.Error
		if len(errStr) > 60 {
			errStr = errStr[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			r.JobName, r.JobType,
			r.StartedAt.Local().Format("2006-01-02 15:04:05"),
			r.Duration.Round(time.Millisecond),
			r.Status, r.Attempt, errStr)
	}
	w.Flush()

	return nil
}
