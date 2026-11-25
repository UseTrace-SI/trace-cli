package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/blob"
	"github.com/UseTrace-SI/trace-cli/internal/engine"
	"github.com/UseTrace-SI/trace-cli/internal/manifest"
)

type uploadPreview struct {
	Files            int      `json:"files"`
	Objects          int      `json:"objects"`
	Rows             int64    `json:"rows"`
	PotentialSecrets []string `json:"potential_secret_paths"`
	// ExcludedHistory lists both of the following, for older readers.
	ExcludedHistory []string `json:"historical_paths_now_excluded"`
	// LeftOut are historical paths under built-in exclusions; uploads leave
	// their snapshots out. StillUploaded are paths .traceignore excludes from
	// new captures whose earlier snapshots are still uploaded.
	LeftOut        []string `json:"historical_paths_left_out"`
	StillUploaded  []string `json:"historical_paths_still_uploaded"`
	SampledBytes   int64    `json:"sampled_bytes"`
	ScanIncomplete bool     `json:"scan_incomplete"`
	Disclosure     string   `json:"disclosure"`
}

func previewUpload(ctx context.Context, e *engine.Engine) (uploadPreview, error) {
	out := uploadPreview{PotentialSecrets: []string{}, ExcludedHistory: []string{}, LeftOut: []string{}, StillUploaded: []string{}, Disclosure: "Approval uploads existing and future history and derived context. Source bytes remain exact and may contain credentials. Hosted personal history is server-encrypted and readable by the service. Earlier snapshots of files matching Trace's built-in credential exclusions are left out of uploads; .traceignore changes do not erase earlier snapshots. Secret scanning is best effort."}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := e.DB.QueryContext(ctx, "SELECT manifest_hash FROM keyframes")
	if err != nil {
		return out, err
	}
	var hashes []blob.Hash
	for rows.Next() {
		var h blob.Hash
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return out, err
		}
		hashes = append(hashes, h)
		if len(hashes) > 100000 {
			rows.Close()
			return out, fmt.Errorf("preview exceeds 100000 keyframes")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	files := map[string]bool{}
	scanned := map[blob.Hash]bool{}
	suspect := map[string]bool{}
	leftOut, stillUploaded := map[string]bool{}, map[string]bool{}
	for _, hash := range hashes {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		m, err := manifest.Load(hash, e.Blobs)
		if err != nil {
			return out, err
		}
		for path, entry := range m {
			// Uploads leave built-in exclusions out entirely (see
			// syncclient.referencedHashes), so they are neither counted nor
			// scanned.
			if manifest.Excluded(path) {
				leftOut[path] = true
				continue
			}
			files[path] = true
			omit, err := manifest.CaptureExcluded(e.Root, path)
			if err != nil {
				return out, err
			}
			if omit {
				stillUploaded[path] = true
			}
			if scanned[entry.Hash] {
				continue
			}
			scanned[entry.Hash] = true
			if out.SampledBytes >= 20<<20 {
				out.ScanIncomplete = true
				continue
			}
			r, err := e.Blobs.Get(entry.Hash)
			if err != nil {
				return out, err
			}
			b, readErr := io.ReadAll(io.LimitReader(r, min(1<<20, 20<<20-out.SampledBytes)+1))
			closeErr := r.Close()
			if readErr != nil {
				return out, readErr
			}
			if closeErr != nil {
				return out, closeErr
			}
			if len(b) > 1<<20 {
				out.ScanIncomplete = true
				b = b[:1<<20]
			}
			out.SampledBytes += int64(len(b))
			if e.Redactor.Text(string(b)) != string(b) {
				suspect[path] = true
			}
		}
	}
	out.Files = len(files)
	out.Objects = len(scanned)
	for path := range suspect {
		out.PotentialSecrets = append(out.PotentialSecrets, path)
	}
	sort.Strings(out.PotentialSecrets)
	for path := range leftOut {
		out.LeftOut = append(out.LeftOut, path)
	}
	for path := range stillUploaded {
		out.StillUploaded = append(out.StillUploaded, path)
	}
	sort.Strings(out.LeftOut)
	sort.Strings(out.StillUploaded)
	out.ExcludedHistory = append(append(out.ExcludedHistory, out.LeftOut...), out.StillUploaded...)
	sort.Strings(out.ExcludedHistory)
	for _, table := range []string{"sessions", "turns", "keyframes", "ops", "commits", "messages", "comments", "trails", "sync_updates", "context_snapshots"} {
		var n int64
		if err := e.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			return out, err
		}
		out.Rows += n
	}
	return out, nil
}

func printUploadPreview(ctx context.Context, root string, out io.Writer, jsonOutput bool) error {
	e, err := engine.Open(root)
	if err != nil {
		return err
	}
	defer e.Close()
	preview, err := previewUpload(ctx, e)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(out).Encode(preview)
	}
	if _, err := fmt.Fprintf(out, "Upload preview: %d files, %d file versions, %d history rows.\n%s\n", preview.Files, preview.Objects, preview.Rows, preview.Disclosure); err != nil {
		return err
	}
	for _, path := range preview.PotentialSecrets {
		if _, err := fmt.Fprintf(out, "Potential credential in historical file: %s\n", path); err != nil {
			return err
		}
	}
	for _, path := range preview.LeftOut {
		if _, err := fmt.Fprintf(out, "Credential file in earlier history, left out of uploads: %s\n", path); err != nil {
			return err
		}
	}
	for _, path := range preview.StillUploaded {
		if _, err := fmt.Fprintf(out, "Excluded by .traceignore from future capture, earlier snapshots still uploaded: %s\n", path); err != nil {
			return err
		}
	}
	if preview.ScanIncomplete {
		_, err = fmt.Fprintln(out, "Secret scan sampled bounded content; inspect other source files before connecting.")
	}
	return err
}
