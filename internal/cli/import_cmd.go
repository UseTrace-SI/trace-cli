package cli

import (
	"encoding/json"
	"fmt"

	"github.com/UseTrace-SI/trace-cli/internal/companions"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/interop"
	"github.com/spf13/cobra"
)

func importCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var source, bundle string
	cmd := &cobra.Command{Use: "import", Short: "Import history from another Trace database", RunE: func(c *cobra.Command, _ []string) error {
		if (source == "") == (bundle == "") {
			return fmt.Errorf("choose exactly one of --from or --bundle")
		}
		root, err := initializedRoot(*repoPath)
		if err != nil {
			return err
		}
		switch source {
		case "git-ai":
			return importGitAI(c, root, *jsonOutput)
		case "entire":
			return fmt.Errorf("importing from Entire is not supported: its checkpoint file layout and trailer value are not publicly specified (pass ./entire to import a Trace database file named entire)")
		}
		if bundle != "" {
			bundleReport, err := readBundleReport(c.Context(), root, bundle)
			if err != nil {
				return err
			}
			report := companions.Refresh(c.Context(), root)
			_ = companions.SaveStatus(root, report)
			if *jsonOutput {
				return json.NewEncoder(c.OutOrStdout()).Encode(bundleReport)
			}
			fmt.Fprintf(c.OutOrStdout(), "imported bundle %s (brain: %s, graph: %s)\n", bundle, report.Brain.Status, report.Graph.Status)
			return nil
		}
		if source == "" {
			return fmt.Errorf("--from is required")
		}
		reportData, err := importDatabase(c.Context(), root, source)
		if err != nil {
			return err
		}

		report := companions.Refresh(c.Context(), root)
		_ = companions.SaveStatus(root, report)
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(reportData)
		}
		fmt.Fprintf(c.OutOrStdout(), "imported %d rows, skipped %d duplicates from %s (brain: %s, graph: %s)\n", reportData.Imported, reportData.Skipped, source, report.Brain.Status, report.Graph.Status)
		return nil
	}}
	cmd.Flags().StringVar(&source, "from", "", "source Trace database to import, or git-ai to read git-ai notes (refs/notes/ai) read-only")
	cmd.Flags().StringVar(&bundle, "bundle", "", "bundle file to import")
	return cmd
}

func importGitAI(c *cobra.Command, root string, jsonOutput bool) error {
	e, err := engine.Open(root)
	if err != nil {
		return err
	}
	defer e.Close()
	report, err := interop.ImportGitAI(e.DB.DB, root)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(c.OutOrStdout()).Encode(report)
	}
	_, err = fmt.Fprintf(c.OutOrStdout(), "read %d git-ai notes from %s: %d new range hints, %d already imported, %d notes skipped\n",
		report.Notes, report.Ref, report.Imported, report.Existing, len(report.Skipped))
	for _, s := range report.Skipped {
		if err == nil {
			_, err = fmt.Fprintf(c.OutOrStdout(), "  skipped %s\n", s)
		}
	}
	return err
}
