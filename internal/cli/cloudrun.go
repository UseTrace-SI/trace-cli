package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func cloudRunCommand(repoPath *string, jsonOutput *bool) *cobra.Command {
	var platformURL, prompt string
	cmd := &cobra.Command{Use: "cloud-run", Short: "Submit a job to the Trace cloud runner (stub)", RunE: func(c *cobra.Command, _ []string) error {
		if platformURL == "" {
			platformURL = os.Getenv("TRACE_PLATFORM_URL")
			if platformURL == "" {
				platformURL = "https://usetrace.si"
			}
		}
		body, _ := json.Marshal(map[string]any{"prompt": prompt, "repo": *repoPath})
		resp, err := http.Post(platformURL+"/api/runner/jobs", "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var job map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(c.OutOrStdout()).Encode(job)
		}
		fmt.Fprintf(c.OutOrStdout(), "submitted cloud job: %v (status: %v)\n", job["id"], job["status"])
		// Poll briefly.
		for i := 0; i < 5; i++ {
			time.Sleep(500 * time.Millisecond)
			r, err := http.Get(platformURL + "/api/runner/jobs")
			if err != nil {
				break
			}
			r.Body.Close()
		}
		return nil
	}}
	cmd.Flags().StringVar(&platformURL, "platform", "", "platform base URL")
	cmd.Flags().StringVar(&prompt, "prompt", "", "prompt to run")
	return cmd
}